package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	// ErrUnauthorized is returned when a user lacks the required role for an operation
	ErrUnauthorized = errors.New("unauthorized: insufficient permissions")
	// ErrInvalidRole is returned when an invalid role string is provided
	ErrInvalidRole = errors.New("invalid role")
)

// Role represents a predefined role in the system
type Role string

const (
	// RoleAdmin has full system access including user management and configuration
	RoleAdmin Role = "admin"
	// RoleOperator can perform operational tasks like deployments and monitoring
	RoleOperator Role = "operator"
	// RoleAuditor has read-only access for compliance and audit purposes
	RoleAuditor Role = "auditor"
	// RoleService represents service-to-service authentication
	RoleService Role = "service"
	// RoleTenantOwner has full access within a tenant including user management
	RoleTenantOwner Role = "tenant-owner"
	// RolePlatformAdmin has cross-tenant access and tenant provisioning capabilities
	RolePlatformAdmin Role = "platform-admin"
	// RoleSuperAdmin has unrestricted platform-wide access
	RoleSuperAdmin Role = "super-admin"
)

// String returns the string representation of a Role
func (r Role) String() string {
	return string(r)
}

// IsValid checks if a role string is a valid predefined role
func (r Role) IsValid() bool {
	switch r {
	case RoleAdmin, RoleOperator, RoleAuditor, RoleService,
		RoleTenantOwner, RolePlatformAdmin, RoleSuperAdmin:
		return true
	default:
		return false
	}
}

// roleHierarchy defines which roles a granter role is permitted to assign.
// A role may only grant roles listed in its slice.
// Unexported to prevent external mutation; use CanGrantRole or GetGrantableRoles for access.
var roleHierarchy = map[Role][]Role{
	RolePlatformAdmin: {RoleTenantOwner, RoleAdmin, RoleOperator, RoleAuditor},
	RoleTenantOwner:   {RoleAdmin, RoleOperator, RoleAuditor},
	RoleAdmin:         {RoleOperator, RoleAuditor},
}

// GetGrantableRoles returns a copy of the roles that the given granter role is permitted to assign.
// Returns nil if the granter role has no delegation authority.
func GetGrantableRoles(granter Role) []Role {
	grantable := roleHierarchy[granter]
	if len(grantable) == 0 {
		return nil
	}
	result := make([]Role, len(grantable))
	copy(result, grantable)
	return result
}

// CanGrantRole reports whether any role in granterRoles is permitted to assign targetRole.
func CanGrantRole(granterRoles []Role, targetRole Role) bool {
	for _, granter := range granterRoles {
		for _, grantable := range roleHierarchy[granter] {
			if grantable == targetRole {
				return true
			}
		}
	}
	return false
}

// Permission represents an action that can be performed on a resource
type Permission string

const (
	// PermissionRead allows reading resources
	PermissionRead Permission = "read"
	// PermissionWrite allows creating and updating resources
	PermissionWrite Permission = "write"
	// PermissionDelete allows deleting resources
	PermissionDelete Permission = "delete"
	// PermissionExecute allows executing operations
	PermissionExecute Permission = "execute"
)

// ResourceType represents a category of resources in the system
type ResourceType string

const (
	// ResourceTypeAccount represents account-related resources
	ResourceTypeAccount ResourceType = "account"
	// ResourceTypePosition represents position-related resources
	ResourceTypePosition ResourceType = "position"
	// ResourceTypeTransaction represents transaction-related resources
	ResourceTypeTransaction ResourceType = "transaction"
	// ResourceTypeAudit represents audit log resources
	ResourceTypeAudit ResourceType = "audit"
	// ResourceTypeSystem represents system configuration resources
	ResourceTypeSystem ResourceType = "system"
	// ResourceTypeIdentity represents identity and user management resources
	ResourceTypeIdentity ResourceType = "identity"
)

// rolePermissions defines the permissions for each role
var rolePermissions = map[Role]map[ResourceType][]Permission{
	RoleAdmin: {
		ResourceTypeAccount:     {PermissionRead, PermissionWrite, PermissionDelete, PermissionExecute},
		ResourceTypePosition:    {PermissionRead, PermissionWrite, PermissionDelete, PermissionExecute},
		ResourceTypeTransaction: {PermissionRead, PermissionWrite, PermissionDelete, PermissionExecute},
		ResourceTypeAudit:       {PermissionRead, PermissionWrite, PermissionDelete, PermissionExecute},
		ResourceTypeSystem:      {PermissionRead, PermissionWrite, PermissionDelete, PermissionExecute},
	},
	RoleOperator: {
		ResourceTypeAccount:     {PermissionRead, PermissionWrite, PermissionExecute},
		ResourceTypePosition:    {PermissionRead, PermissionWrite, PermissionExecute},
		ResourceTypeTransaction: {PermissionRead, PermissionWrite, PermissionExecute},
		ResourceTypeAudit:       {PermissionRead},
		ResourceTypeSystem:      {PermissionRead},
	},
	RoleAuditor: {
		ResourceTypeAccount:     {PermissionRead},
		ResourceTypePosition:    {PermissionRead},
		ResourceTypeTransaction: {PermissionRead},
		ResourceTypeAudit:       {PermissionRead},
		ResourceTypeSystem:      {PermissionRead},
	},
	RoleService: {
		ResourceTypeAccount:     {PermissionRead, PermissionWrite},
		ResourceTypePosition:    {PermissionRead, PermissionWrite},
		ResourceTypeTransaction: {PermissionRead, PermissionWrite},
		ResourceTypeAudit:       {PermissionWrite},
		ResourceTypeSystem:      {PermissionRead},
	},
	// RoleTenantOwner: same as admin plus identity/user management within the tenant
	RoleTenantOwner: {
		ResourceTypeAccount:     {PermissionRead, PermissionWrite, PermissionDelete, PermissionExecute},
		ResourceTypePosition:    {PermissionRead, PermissionWrite, PermissionDelete, PermissionExecute},
		ResourceTypeTransaction: {PermissionRead, PermissionWrite, PermissionDelete, PermissionExecute},
		ResourceTypeAudit:       {PermissionRead, PermissionWrite, PermissionDelete, PermissionExecute},
		ResourceTypeSystem:      {PermissionRead, PermissionWrite, PermissionDelete, PermissionExecute},
		ResourceTypeIdentity:    {PermissionRead, PermissionWrite, PermissionDelete, PermissionExecute},
	},
	// RolePlatformAdmin: cross-tenant access and tenant provisioning
	RolePlatformAdmin: {
		ResourceTypeAccount:     {PermissionRead, PermissionWrite, PermissionDelete, PermissionExecute},
		ResourceTypePosition:    {PermissionRead, PermissionWrite, PermissionDelete, PermissionExecute},
		ResourceTypeTransaction: {PermissionRead, PermissionWrite, PermissionDelete, PermissionExecute},
		ResourceTypeAudit:       {PermissionRead, PermissionWrite, PermissionDelete, PermissionExecute},
		ResourceTypeSystem:      {PermissionRead, PermissionWrite, PermissionDelete, PermissionExecute},
		ResourceTypeIdentity:    {PermissionRead, PermissionWrite, PermissionDelete, PermissionExecute},
	},
}

// HasAnyRole checks if the claims contain any of the specified roles
func HasAnyRole(claims *Claims, roles ...Role) bool {
	if claims == nil {
		return false
	}

	for _, role := range roles {
		if claims.HasRole(role.String()) {
			return true
		}
	}
	return false
}

// HasPermission checks if the claims have a specific permission for a resource type
func HasPermission(claims *Claims, resourceType ResourceType, permission Permission) bool {
	if claims == nil {
		return false
	}

	// Check each role in the claims
	for _, roleStr := range claims.Roles {
		role := Role(roleStr)
		if !role.IsValid() {
			continue
		}

		// Get permissions for this role and resource type
		if resourcePermissions, exists := rolePermissions[role]; exists {
			if permissions, exists := resourcePermissions[resourceType]; exists {
				for _, p := range permissions {
					if p == permission {
						return true
					}
				}
			}
		}
	}

	return false
}

// CheckRole returns an error if the claims don't contain the required role
func CheckRole(claims *Claims, role Role) error {
	if !claims.HasRole(role.String()) {
		return fmt.Errorf("%w: required role '%s' not found", ErrUnauthorized, role)
	}
	return nil
}

// CheckAnyRole returns an error if the claims don't contain any of the required roles
func CheckAnyRole(claims *Claims, roles ...Role) error {
	if !HasAnyRole(claims, roles...) {
		roleNames := make([]string, len(roles))
		for i, r := range roles {
			roleNames[i] = r.String()
		}
		return fmt.Errorf("%w: required one of roles [%s]", ErrUnauthorized, strings.Join(roleNames, ", "))
	}
	return nil
}

// CheckPermission returns an error if the claims don't have the required permission
func CheckPermission(claims *Claims, resourceType ResourceType, permission Permission) error {
	if !HasPermission(claims, resourceType, permission) {
		return fmt.Errorf("%w: required permission '%s' for resource '%s'", ErrUnauthorized, permission, resourceType)
	}
	return nil
}

// HTTPAuthorizationMiddleware returns HTTP middleware that enforces role-based authorization
type HTTPAuthorizationMiddleware struct {
	requiredRoles []Role
}

// NewHTTPAuthorizationMiddleware creates a new HTTP authorization middleware
func NewHTTPAuthorizationMiddleware(roles ...Role) *HTTPAuthorizationMiddleware {
	return &HTTPAuthorizationMiddleware{
		requiredRoles: roles,
	}
}

// Handler returns an http.Handler that enforces role requirements
func (m *HTTPAuthorizationMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := GetClaimsFromContext(r.Context())
		if !ok {
			http.Error(w, "Unauthorized: no claims in context", http.StatusUnauthorized)
			return
		}

		if len(m.requiredRoles) > 0 {
			if err := CheckAnyRole(claims, m.requiredRoles...); err != nil {
				http.Error(w, err.Error(), http.StatusForbidden)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// RequireRoleUnary returns a gRPC unary interceptor that enforces role-based authorization
// This complements the existing RequireRole function by working with the new Role type
func RequireRoleUnary(roles ...Role) grpc.UnaryServerInterceptor {
	// Convert Role to string for compatibility with existing RequireRole
	roleStrings := make([]string, len(roles))
	for i, role := range roles {
		roleStrings[i] = role.String()
	}
	return RequireRole(roleStrings...)
}

// RequireRoleStream returns a gRPC stream interceptor that enforces role-based authorization
func RequireRoleStream(roles ...Role) grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		_ *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		claims, ok := GetClaimsFromContext(ss.Context())
		if !ok {
			return status.Error(codes.Unauthenticated, "missing authentication context")
		}

		if len(roles) > 0 {
			if err := CheckAnyRole(claims, roles...); err != nil {
				return status.Error(codes.PermissionDenied, err.Error())
			}
		}

		return handler(srv, ss)
	}
}

// ParseRoles converts a slice of strings to validated roles
func ParseRoles(roleStrings []string) ([]Role, error) {
	roles := make([]Role, 0, len(roleStrings))
	for _, rs := range roleStrings {
		role := Role(rs)
		if !role.IsValid() {
			return nil, fmt.Errorf("%w: '%s'", ErrInvalidRole, rs)
		}
		roles = append(roles, role)
	}
	return roles, nil
}

// AuthorizeResource is a helper that checks if claims have permission for a resource
// This is the main entry point for resource-level authorization
func AuthorizeResource(ctx context.Context, resourceType ResourceType, permission Permission) error {
	claims, ok := GetClaimsFromContext(ctx)
	if !ok {
		return ErrUnauthorized
	}

	return CheckPermission(claims, resourceType, permission)
}

// AuthorizeAccountRead checks if the claims allow reading account resources
func AuthorizeAccountRead(ctx context.Context) error {
	return AuthorizeResource(ctx, ResourceTypeAccount, PermissionRead)
}

// AuthorizeAccountWrite checks if the claims allow writing account resources
func AuthorizeAccountWrite(ctx context.Context) error {
	return AuthorizeResource(ctx, ResourceTypeAccount, PermissionWrite)
}

// AuthorizePositionRead checks if the claims allow reading position resources
func AuthorizePositionRead(ctx context.Context) error {
	return AuthorizeResource(ctx, ResourceTypePosition, PermissionRead)
}

// AuthorizePositionWrite checks if the claims allow writing position resources
func AuthorizePositionWrite(ctx context.Context) error {
	return AuthorizeResource(ctx, ResourceTypePosition, PermissionWrite)
}

// AuthorizeTransactionRead checks if the claims allow reading transaction resources
func AuthorizeTransactionRead(ctx context.Context) error {
	return AuthorizeResource(ctx, ResourceTypeTransaction, PermissionRead)
}

// AuthorizeTransactionWrite checks if the claims allow writing transaction resources
func AuthorizeTransactionWrite(ctx context.Context) error {
	return AuthorizeResource(ctx, ResourceTypeTransaction, PermissionWrite)
}

// AuthorizeAuditRead checks if the claims allow reading audit resources
func AuthorizeAuditRead(ctx context.Context) error {
	return AuthorizeResource(ctx, ResourceTypeAudit, PermissionRead)
}

// AuthorizeSystemRead checks if the claims allow reading system configuration
func AuthorizeSystemRead(ctx context.Context) error {
	return AuthorizeResource(ctx, ResourceTypeSystem, PermissionRead)
}

// AuthorizeSystemWrite checks if the claims allow writing system configuration
func AuthorizeSystemWrite(ctx context.Context) error {
	return AuthorizeResource(ctx, ResourceTypeSystem, PermissionWrite)
}
