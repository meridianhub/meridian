# RBAC Testing Guide

This guide demonstrates how to test the Role-Based Access Control (RBAC) system locally using Tilt.

## Overview

The RBAC system provides four predefined roles:

- **admin**: Full system access
- **operator**: Operational tasks (read/write most resources, read-only audit/system)
- **auditor**: Read-only access for compliance
- **service**: Service-to-service authentication

## Local Testing with Keycloak

### 1. Configure Keycloak Roles

Add these roles to your Keycloak realm:

```bash

# In your Keycloak admin console or via REST API:

- admin
- operator
- auditor
- service

```

### 2. Create Test Users

Create users with different roles for testing:

**Admin User:**

- Username: `admin@example.com`
- Roles: `admin`

**Operator User:**

- Username: `operator@example.com`
- Roles: `operator`

**Auditor User:**

- Username: `auditor@example.com`
- Roles: `auditor`

**Service Account:**

- Client ID: `test-service`
- Roles: `service`

### 3. Testing Permission Checks

#### HTTP Middleware Example

```go
// In your HTTP server setup
import "github.com/meridianhub/meridian/internal/platform/auth"

// Protect an endpoint requiring admin role
adminMiddleware := auth.NewHTTPAuthorizationMiddleware(auth.RoleAdmin)
http.Handle("/admin/config", adminMiddleware.Handler(configHandler))

// Protect an endpoint requiring operator or admin
opsMiddleware := auth.NewHTTPAuthorizationMiddleware(auth.RoleOperator, auth.RoleAdmin)
http.Handle("/ops/deploy", opsMiddleware.Handler(deployHandler))
```

#### gRPC Interceptor Example

```go
// In your gRPC server setup
import "github.com/meridianhub/meridian/internal/platform/auth"

// Require admin role for sensitive operations
server := grpc.NewServer(
    grpc.UnaryInterceptor(auth.RequireRoleUnary(auth.RoleAdmin)),
)
```

#### Resource-Level Authorisation

```go
// In your service handlers
func (s *AccountService) CreateAccount(ctx context.Context, req *pb.CreateAccountRequest) (*pb.Account, error) {
    // Check if user has write permission for accounts
    if err := auth.AuthorizeAccountWrite(ctx); err != nil {
        return nil, status.Error(codes.PermissionDenied, err.Error())
    }

    // Proceed with account creation
    ...
}

func (s *AuditService) QueryAuditLogs(ctx context.Context, req *pb.QueryRequest) (*pb.AuditLogs, error) {
    // Check if user has read permission for audit logs
    if err := auth.AuthorizeAuditRead(ctx); err != nil {
        return nil, status.Error(codes.PermissionDenied, err.Error())
    }

    // Proceed with query
    ...
}
```

### 4. Manual Testing with curl

#### Get JWT Token

```bash

# Get admin token

ADMIN_TOKEN=$(curl -X POST http://localhost:8080/realms/meridian/protocol/openid-connect/token \
  -d "client_id=test-client" \
  -d "username=admin@example.com" \
  -d "password=admin123" \
  -d "grant_type=password" | jq -r '.access_token')

# Get auditor token

AUDITOR_TOKEN=$(curl -X POST http://localhost:8080/realms/meridian/protocol/openid-connect/token \
  -d "client_id=test-client" \
  -d "username=auditor@example.com" \
  -d "password=auditor123" \
  -d "grant_type=password" | jq -r '.access_token')
```

#### Test Endpoints

```bash

# Admin can access protected endpoint (should succeed)

curl -H "Authorization: Bearer $ADMIN_TOKEN" \
  http://localhost:8081/api/admin/config

# Auditor cannot access admin endpoint (should return 403)

curl -H "Authorization: Bearer $AUDITOR_TOKEN" \
  http://localhost:8081/api/admin/config

# Auditor can read accounts (should succeed)

curl -H "Authorization: Bearer $AUDITOR_TOKEN" \
  http://localhost:8081/api/accounts

# Auditor cannot create accounts (should return 403)

curl -X POST -H "Authorization: Bearer $AUDITOR_TOKEN" \
  -d '{"name":"Test Account"}' \
  http://localhost:8081/api/accounts
```

### 5. Testing with grpcurl

```bash

# Admin can call admin-only gRPC method

grpcurl -H "authorization: Bearer $ADMIN_TOKEN" \
  localhost:9090 \
  meridian.v1.ConfigService/UpdateSystemConfig

# Auditor cannot call admin-only method (should return PERMISSION_DENIED)

grpcurl -H "authorization: Bearer $AUDITOR_TOKEN" \
  localhost:9090 \
  meridian.v1.ConfigService/UpdateSystemConfig
```

## Permission Matrix

| Role      | Account | Position | Transaction | Audit   | System  |
|-----------|---------|----------|-------------|---------|---------|
| admin     | CRUD+E  | CRUD+E   | CRUD+E      | CRUD+E  | CRUD+E  |
| operator  | RW+E    | RW+E     | RW+E        | R       | R       |
| auditor   | R       | R        | R           | R       | R       |
| service   | RW      | RW       | RW          | W       | R       |

Legend:

- C: Create
- R: Read
- U: Update
- D: Delete
- E: Execute
- W: Write (Create + Update)

## Troubleshooting

### 403 Forbidden

Check that:

1. JWT token contains the required role in the `roles` claim
2. Token is not expired
3. Authorisation header is properly formatted: `Authorization: Bearer <token>`

### 401 Unauthorized

Check that:

1. JWT token is included in the request
2. Token signature is valid
3. Keycloak public key is correctly configured

### Debugging

Enable debug logging to see authorisation decisions:

```go
import "github.com/meridianhub/meridian/internal/platform/observability"

logger := observability.NewLogger(os.Stdout, observability.LogLevelDebug)
logger.DebugContext(ctx, "Authorisation check", map[string]interface{}{
    "user_id": claims.UserID,
    "roles": claims.Roles,
    "required_role": auth.RoleAdmin.String(),
    "has_role": auth.HasAnyRole(claims, auth.RoleAdmin),
})
```

## Integration with Existing Services

The RBAC system integrates seamlessly with existing JWT authentication:

```go
// In your server setup
authMiddleware := NewJWTAuthenticationMiddleware(jwksClient, validator)
rbacMiddleware := auth.NewHTTPAuthorizationMiddleware(auth.RoleAdmin)

// Chain middleware
http.Handle("/admin/endpoint",
    authMiddleware.Handler(  // First: Authenticate and extract claims
        rbacMiddleware.Handler(  // Second: Check roles
            yourHandler,
        ),
    ),
)
```

## Best Practices

1. **Use type-safe Role constants** instead of strings: `auth.RoleAdmin` not `"admin"`
2. **Check permissions at service layer** not just at API boundaries
3. **Use resource-level helpers** for clarity: `AuthorizeAccountWrite(ctx)` is clearer than `HasPermission(claims,
ResourceTypeAccount, PermissionWrite)`
4. **Test with all roles** during development
5. **Log authorisation failures** for security monitoring
