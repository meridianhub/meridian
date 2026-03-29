# Authentication Integration Guide

This guide shows how to integrate JWT authentication into your Meridian services using the auth platform package.

## Table of Contents

- [Quick Start](#quick-start)
- [Configuration](#configuration)
- [Service Integration](#service-integration)
- [Extracting User Information](#extracting-user-information)
- [Testing with Keycloak](#testing-with-keycloak)
- [Production Deployment](#production-deployment)
- [Troubleshooting](#troubleshooting)

## Quick Start

### 1. Add Auth Configuration to Your Service

```go
package main

import (
    "context"
    "log"

    "github.com/meridianhub/meridian/internal/platform/auth"
    "google.golang.org/grpc"
)

func main() {
    ctx := context.Background()

    // Load auth config from environment
    authConfig, err := auth.NewConfigFromEnv()
    if err != nil {
        log.Fatalf("Failed to load auth config: %v", err)
    }

    // Create authenticator (interceptor)
    authenticator, err := authConfig.NewAuthenticator(ctx)
    if err != nil {
        log.Fatalf("Failed to create authenticator: %v", err)
    }
    defer authenticator.Close()

    // Create gRPC server with auth interceptors
    grpcServer := grpc.NewServer(
        grpc.ChainUnaryInterceptor(
            authenticator.UnaryInterceptor(),
        ),
        grpc.ChainStreamInterceptor(
            authenticator.StreamInterceptor(),
        ),
    )

    // Register your services...
    // pb.RegisterYourServiceServer(grpcServer, &yourService{})

    // Start server...
}
```

### 2. Set Environment Variables

For local development (Tilt):

```bash
export AUTH_MODE=jwks
export JWKS_URL=http://keycloak:8080/realms/meridian/protocol/openid-connect/certs
export JWKS_CACHE_TTL=1h
export JWKS_REFRESH_TTL=30m
```

For production:

```bash
export AUTH_MODE=jwks
export JWKS_URL=https://your-auth-provider.com/.well-known/jwks.json
export JWKS_CACHE_TTL=24h
export JWKS_REFRESH_TTL=12h
```

### 3. Extract User Information in Handlers

```go
import "github.com/meridianhub/meridian/internal/platform/auth"

func (s *yourService) YourMethod(ctx context.Context, req *pb.Request) (*pb.Response, error) {
    // Get authenticated user ID
    userID, ok := auth.GetUserIDFromContext(ctx)
    if !ok {
        return nil, status.Error(codes.Unauthenticated, "user not authenticated")
    }

    // Use userID for database operations
    record := &YourRecord{
        Data:      req.Data,
        CreatedBy: userID,
        UpdatedBy: userID,
    }

    // Get full claims if needed
    claims, ok := auth.GetClaimsFromContext(ctx)
    if !ok {
        return nil, status.Error(codes.Unauthenticated, "claims not found")
    }

    // Check roles
    roles := claims.GetRoles()
    if !contains(roles, "admin") {
        return nil, status.Error(codes.PermissionDenied, "admin role required")
    }

    // Your business logic...
    return &pb.Response{}, nil
}
```

## Configuration

### Environment Variables

| Variable     | Required        | Default | Description                                             |
|--------------|-----------------|---------|--------------------------------------------------------|
| `AUTH_MODE`  | No              | `jwks`  | Authentication mode: `jwks`, `oauth`, or `disabled`     |
| `JWKS_URL`   | Yes (JWKS mode) | -       | JWKS endpoint URL                                       |
| `JWKS_CACHE_TTL` | No | `24h` | How long to cache JWKS keys |
| `JWKS_REFRESH_TTL` | No | - | Background refresh interval (optional) |
| `OAUTH_CLIENT_ID` | Yes (OAuth mode) | - | OAuth client ID |
| `OAUTH_CLIENT_SECRET` | Yes (OAuth mode) | - | OAuth client secret |
| `OAUTH_TOKEN_URL` | Yes (OAuth mode) | - | OAuth token endpoint |
| `OAUTH_SCOPES` | No | - | Comma-separated OAuth scopes |
| `OAUTH_INTROSPECTION_URL` | No | - | Token introspection endpoint (optional) |

### Configuration Modes

#### JWKS Mode (Recommended for Inbound Authentication)

Best for validating JWTs from external identity providers:

```go
config := auth.Config{
    Mode:           auth.AuthModeJWKS,
    JWKSURL:        "https://auth.example.com/.well-known/jwks.json",
    JWKSCacheTTL:   24 * time.Hour,
    JWKSRefreshTTL: 12 * time.Hour,
}
```

#### OAuth Mode (For Service-to-Service Communication)

Best for outbound authentication when calling other services:

```go
config := auth.Config{
    Mode:              auth.AuthModeOAuth,
    OAuthClientID:     "your-service",
    OAuthClientSecret: "your-secret",
    OAuthTokenURL:     "https://auth.example.com/oauth/token",
    OAuthScopes:       []string{"read:data", "write:data"},
}

// Create OAuth client for outbound calls
oauthClient, err := config.NewOAuthClient()
if err != nil {
    log.Fatal(err)
}

// Get token for service-to-service call
token, err := oauthClient.GetToken(ctx)
if err != nil {
    log.Fatal(err)
}

// Use token in outbound gRPC call
md := metadata.Pairs("authorization", "Bearer "+token)
ctx = metadata.NewOutgoingContext(ctx, md)
```

#### Disabled Mode (Testing Only)

Disables authentication for testing:

```go
config := auth.Config{
    Mode: auth.AuthModeDisabled,
}
```

**⚠️ Never use disabled mode in production!**

## Service Integration

### Full Service Example

```go
package main

import (
    "context"
    "fmt"
    "log"
    "net"

    "github.com/meridianhub/meridian/internal/platform/auth"
    pb "github.com/meridianhub/meridian/api/gen/go/meridian/v1"
    "google.golang.org/grpc"
    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/status"
)

type accountService struct {
    pb.UnimplementedAccountServiceServer
}

func (s *accountService) CreateAccount(ctx context.Context, req *pb.CreateAccountRequest) (*pb.CreateAccountResponse,
error) {
    // Extract authenticated user
    userID, ok := auth.GetUserIDFromContext(ctx)
    if !ok {
        return nil, status.Error(codes.Unauthenticated, "user not authenticated")
    }

    // Get claims for authorization
    claims, ok := auth.GetClaimsFromContext(ctx)
    if !ok {
        return nil, status.Error(codes.Unauthenticated, "claims not found")
    }

    // Check if user has required role
    roles := claims.GetRoles()
    if !hasRole(roles, "account-creator") {
        return nil, status.Error(codes.PermissionDenied, "insufficient permissions")
    }

    // Business logic - create account with audit fields
    account := &Account{
        Name:      req.Name,
        CreatedBy: userID,
        UpdatedBy: userID,
    }

    // Save to database...

    return &pb.CreateAccountResponse{
        AccountId: account.ID,
    }, nil
}

func main() {
    ctx := context.Background()

    // Load configuration from environment
    authConfig, err := auth.NewConfigFromEnv()
    if err != nil {
        log.Fatalf("Failed to load auth config: %v", err)
    }

    // Create authenticator
    authenticator, err := authConfig.NewAuthenticator(ctx)
    if err != nil {
        log.Fatalf("Failed to create authenticator: %v", err)
    }
    defer func() {
        if err := authenticator.Close(); err != nil {
            log.Printf("Error closing authenticator: %v", err)
        }
    }()

    // Create gRPC server with auth
    grpcServer := grpc.NewServer(
        grpc.ChainUnaryInterceptor(
            authenticator.UnaryInterceptor(),
        ),
        grpc.ChainStreamInterceptor(
            authenticator.StreamInterceptor(),
        ),
    )

    // Register services
    pb.RegisterAccountServiceServer(grpcServer, &accountService{})

    // Start server
    lis, err := net.Listen("tcp", ":9090")
    if err != nil {
        log.Fatalf("Failed to listen: %v", err)
    }

    fmt.Println("Server listening on :9090")
    if err := grpcServer.Serve(lis); err != nil {
        log.Fatalf("Failed to serve: %v", err)
    }
}

func hasRole(roles []string, required string) bool {
    for _, role := range roles {
        if role == required {
            return true
        }
    }
    return false
}
```

## Extracting User Information

### Available Context Helpers

```go
// Get user ID (subject claim from JWT)
userID, ok := auth.GetUserIDFromContext(ctx)

// Get user roles
roles, ok := auth.GetRolesFromContext(ctx)

// Get OAuth scopes
scopes, ok := auth.GetScopesFromContext(ctx)

// Get full claims object
claims, ok := auth.GetClaimsFromContext(ctx)
```

### Claims Structure

```go
type Claims struct {
    UserID string   // JWT "sub" claim
    Roles  []string // Custom "roles" claim
    Scopes []string // Custom "scopes" claim or OAuth "scope" claim

    // Standard JWT claims
    jwt.RegisteredClaims // Includes: Issuer, Subject, Audience, ExpiresAt, etc.
}
```

### Using Claims for Authorization

```go
func (s *service) AdminOnlyMethod(ctx context.Context, req *pb.Request) (*pb.Response, error) {
    claims, ok := auth.GetClaimsFromContext(ctx)
    if !ok {
        return nil, status.Error(codes.Unauthenticated, "not authenticated")
    }

    // Check for admin role
    if !claims.HasRole("admin") {
        return nil, status.Error(codes.PermissionDenied, "admin role required")
    }

    // Check for specific scope
    if !claims.HasScope("accounts:write") {
        return nil, status.Error(codes.PermissionDenied, "accounts:write scope required")
    }

    // Proceed with admin operation...
    return &pb.Response{}, nil
}
```

## Testing with Keycloak

### Local Development Setup

When running `tilt up`, Keycloak is automatically configured with:

- **Realm**: `meridian`
- **Admin Console**: <http://localhost:18080> (admin/admin)
- **Test User**: <developer@meridian.local> / developer
- **Client ID**: meridian-service (public client - no secret required)
- **JWKS Endpoint**: <http://localhost:18080/realms/meridian/protocol/openid-connect/certs>

**Note**: The `meridian-service` client is configured as a **public client** for local development convenience. This
allows password grant flow without requiring a client secret. In production, use confidential clients with proper secret
management.

### Getting a Test Token

```bash

# Get access token for test user (no client secret needed for public client)

TOKEN=$(curl -X POST 'http://localhost:18080/realms/meridian/protocol/openid-connect/token' \
  -d 'grant_type=password' \
  -d 'client_id=meridian-service' \
  -d 'username=developer@meridian.local' \
  -d 'password=developer' \
  | jq -r '.access_token')

echo $TOKEN
```

### Making Authenticated gRPC Calls

Using `grpcurl`:

```bash

# Call with Bearer token

grpcurl -H "Authorization: Bearer $TOKEN" \
  -plaintext localhost:9090 \
  meridian.v1.AccountService/CreateAccount \
  -d '{"name": "Test Account"}'
```

Using Go client:

```go
import (
    "google.golang.org/grpc/metadata"
)

// Add token to outgoing context
md := metadata.Pairs("authorization", "Bearer "+token)
ctx := metadata.NewOutgoingContext(context.Background(), md)

// Make call
resp, err := client.CreateAccount(ctx, &pb.CreateAccountRequest{
    Name: "Test Account",
})
```

### Decoding JWT Tokens

View token contents:

```bash

# Decode JWT (header.payload.signature)

echo $TOKEN | cut -d. -f2 | base64 -d | jq
```

Expected claims:

```json
{
  "sub": "user-uuid-here",
  "email": "developer@meridian.local",
  "roles": ["user"],
  "iat": 1234567890,
  "exp": 1234571490,
  "aud": "meridian-service"
}
```

## Production Deployment

### Kubernetes ConfigMap

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: auth-config
data:
  AUTH_MODE: "jwks"
  JWKS_URL: "https://your-auth-provider.com/.well-known/jwks.json"
  JWKS_CACHE_TTL: "24h"
  JWKS_REFRESH_TTL: "12h"
```

### Kubernetes Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: your-service
spec:
  template:
    spec:
      containers:

      - name: service

        image: your-service:latest
        envFrom:

        - configMapRef:

            name: auth-config
        ports:

        - containerPort: 9090

          name: grpc
```

### Security Best Practices

1. **Use HTTPS/TLS**: Always use TLS for JWKS endpoints in production
2. **Set appropriate cache TTLs**: Balance between performance and key rotation
3. **Enable background refresh**: Prevents service interruption during key rotation
4. **Monitor token validation failures**: Track authentication errors
5. **Use OAuth for service-to-service**: Don't share user tokens between services
6. **Rotate secrets regularly**: Change OAuth client secrets periodically
7. **Limit token lifetime**: Use short-lived access tokens (1 hour or less)
8. **Use appropriate scopes**: Grant minimum required permissions

## Troubleshooting

### Common Issues

#### "missing authorization header"

**Cause**: No `Authorization` header in gRPC metadata

**Solution**: Add Bearer token to metadata:

```go
md := metadata.Pairs("authorization", "Bearer "+token)
ctx := metadata.NewOutgoingContext(ctx, md)
```

#### "key not found"

**Cause**: JWT `kid` header doesn't match any key in JWKS

**Solutions**:

- Verify JWKS URL is correct
- Check if key has been rotated
- Trigger manual refresh: restart service or wait for cache expiry

#### "token is expired"

**Cause**: JWT `exp` claim is in the past

**Solution**: Get a new token from identity provider

#### "invalid signature"

**Cause**: JWT signature doesn't match public key

**Solutions**:

- Verify JWKS URL points to correct realm/tenant
- Check if token is from the correct issuer
- Ensure JWKS cache hasn't served stale keys

### Debug Logging

Enable debug logging to see authentication flow:

```go
import "log"

// Before creating authenticator
log.Printf("Auth config: Mode=%s, JWKS=%s", authConfig.Mode, authConfig.JWKSURL)

// In your handler
claims, ok := auth.GetClaimsFromContext(ctx)
if ok {
    log.Printf("Authenticated user: %s, roles: %v", claims.UserID, claims.Roles)
}
```

### Health Checks

Monitor JWKS endpoint availability:

```bash
curl -f http://keycloak:8080/realms/meridian/protocol/openid-connect/certs
```

Check if service can fetch keys:

```bash
kubectl logs -f deployment/your-service | grep -i jwks
```

### Testing Authentication

Test without authentication (should fail):

```bash
grpcurl -plaintext localhost:9090 \
  meridian.v1.AccountService/CreateAccount \
  -d '{"name": "Test"}'

# Expected: Code 16 (Unauthenticated)

```

Test with invalid token (should fail):

```bash
grpcurl -H "Authorization: Bearer invalid-token" \
  -plaintext localhost:9090 \
  meridian.v1.AccountService/CreateAccount \
  -d '{"name": "Test"}'

# Expected: Code 16 (Unauthenticated)

```

Test with valid token (should succeed):

```bash
TOKEN=$(curl -X POST 'http://localhost:18080/realms/meridian/protocol/openid-connect/token' \
  -d 'grant_type=password' \
  -d 'client_id=meridian-service' \
  -d 'username=developer@meridian.local' \
  -d 'password=developer' \
  | jq -r '.access_token')

grpcurl -H "Authorization: Bearer $TOKEN" \
  -plaintext localhost:9090 \
  meridian.v1.AccountService/CreateAccount \
  -d '{"name": "Test"}'

# Expected: Success

```

## Additional Resources

- [JWT.io](https://jwt.io) - Decode and verify JWT tokens
- [Keycloak Documentation](https://www.keycloak.org/documentation)
- [RFC 7519 - JWT](https://datatracker.ietf.org/doc/html/rfc7519)
- [RFC 7517 - JWKS](https://datatracker.ietf.org/doc/html/rfc7517)
- [gRPC Authentication Guide](https://grpc.io/docs/guides/auth/)

## Support

For issues or questions:

1. Check this documentation first
2. Review the [Security Guidelines](../../SECURITY.md)
3. Search existing GitHub issues
4. Create a new issue with:
   - Detailed error messages
   - Configuration (redact secrets!)
   - Steps to reproduce
