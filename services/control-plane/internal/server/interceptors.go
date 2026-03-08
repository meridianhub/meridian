// Package server provides gRPC server interceptors for the control-plane service.
package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/meridianhub/meridian/shared/platform/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// controlPlanePrefix is the gRPC namespace for control-plane services.
const controlPlanePrefix = "/meridian.control_plane.v1."

// roleLevel maps roles to numeric levels for hierarchy comparison.
// Higher numbers indicate more privileged roles.
var roleLevel = map[auth.Role]int{
	auth.RoleAuditor:       1,
	auth.RoleOperator:      2,
	auth.RoleAdmin:         3,
	auth.RoleTenantOwner:   4,
	auth.RolePlatformAdmin: 5,
	auth.RoleSuperAdmin:    6,
	auth.RoleService:       3, // Service accounts have admin-equivalent access
}

// manifestRoleRequirements maps gRPC full method names to the minimum role required.
// Roles are hierarchical: admin > operator > auditor.
// A user with a higher role can access methods requiring a lower role.
//
// IMPORTANT: All control-plane RPCs must be listed here. Unlisted control-plane
// RPCs are denied by the fail-closed check in checkManifestRBAC.
var manifestRoleRequirements = map[string]auth.Role{
	// ManifestHistoryService - read-only, auditor level
	"/meridian.control_plane.v1.ManifestHistoryService/GetCurrentManifest":   auth.RoleAuditor,
	"/meridian.control_plane.v1.ManifestHistoryService/GetManifestVersion":   auth.RoleAuditor,
	"/meridian.control_plane.v1.ManifestHistoryService/ListManifestVersions": auth.RoleAuditor,

	// ApplyManifestService - mutating, admin level
	"/meridian.control_plane.v1.ApplyManifestService/ApplyManifest": auth.RoleAdmin,

	// SagaExecutionService - operational, operator level
	"/meridian.control_plane.v1.SagaExecutionService/ExecuteSaga": auth.RoleOperator,

	// CausationVisualizerService - read-only debugging, auditor level
	"/meridian.control_plane.v1.CausationVisualizerService/GetCausationTreeForPosition":    auth.RoleAuditor,
	"/meridian.control_plane.v1.CausationVisualizerService/GetCausationTreeForTransaction": auth.RoleAuditor,
	"/meridian.control_plane.v1.CausationVisualizerService/GetCausationTreeForEvent":       auth.RoleAuditor,

	// BalanceSheetService - read-only reporting, auditor level
	"/meridian.control_plane.v1.BalanceSheetService/GetBalanceSheet":       auth.RoleAuditor,
	"/meridian.control_plane.v1.BalanceSheetService/GetPositionDetails":    auth.RoleAuditor,
	"/meridian.control_plane.v1.BalanceSheetService/ExportBalanceSheetCSV": auth.RoleAuditor,

	// AuthService - internal service-to-service, service level
	"/meridian.control_plane.v1.AuthService/ValidateAPIKey": auth.RoleService,
}

// meetsMinimumRole checks whether any of the user's roles meets or exceeds
// the required minimum role in the hierarchy.
func meetsMinimumRole(userRoles []string, minimumRole auth.Role) bool {
	requiredLevel, ok := roleLevel[minimumRole]
	if !ok {
		return false
	}

	for _, r := range userRoles {
		role := auth.Role(r)
		if level, exists := roleLevel[role]; exists && level >= requiredLevel {
			return true
		}
	}
	return false
}

// ManifestRBACUnaryInterceptor returns a gRPC unary interceptor that enforces
// role-based access control on control-plane RPCs.
//
// The interceptor fails closed: any control-plane RPC not explicitly listed in
// manifestRoleRequirements is denied. Non-control-plane methods (health, reflection)
// pass through.
func ManifestRBACUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		if err := checkManifestRBAC(ctx, info.FullMethod); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// ManifestRBACStreamInterceptor returns a gRPC stream interceptor that enforces
// role-based access control on control-plane RPCs.
func ManifestRBACStreamInterceptor() grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		if err := checkManifestRBAC(ss.Context(), info.FullMethod); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

// checkManifestRBAC validates that the authenticated user has sufficient role
// for the requested method. Fails closed for unlisted control-plane RPCs.
func checkManifestRBAC(ctx context.Context, fullMethod string) error {
	requiredRole, protected := manifestRoleRequirements[fullMethod]
	if !protected {
		// Fail closed: deny unlisted control-plane RPCs
		if strings.HasPrefix(fullMethod, controlPlanePrefix) {
			return status.Errorf(codes.PermissionDenied,
				"permission denied: no RBAC rule configured for %s", fullMethod)
		}
		return nil
	}

	claims, ok := auth.GetClaimsFromContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "authentication required")
	}

	// Check API key scope enforcement
	if len(claims.Scopes) > 0 {
		if !hasSufficientScope(claims.Scopes, requiredRole) {
			return status.Error(codes.PermissionDenied,
				fmt.Sprintf("API key scope insufficient: requires %s-level access", requiredRole))
		}
	}

	if !meetsMinimumRole(claims.Roles, requiredRole) {
		return status.Error(codes.PermissionDenied,
			fmt.Sprintf("permission denied: %s role required, have roles %v", requiredRole, claims.Roles))
	}

	return nil
}

// requiredScopeLevel maps roles to scope-specific levels, decoupled from roleLevel.
// Only roles used in manifestRoleRequirements need entries here.
var requiredScopeLevel = map[auth.Role]int{
	auth.RoleAuditor:  1,
	auth.RoleOperator: 2,
	auth.RoleAdmin:    3,
}

// manifestScopeLevel maps manifest-specific scope strings to their access level.
var manifestScopeLevel = map[string]int{
	"manifest:read":  1,
	"manifest:write": 2,
	"manifest:admin": 3,
}

// hasSufficientScope checks whether the API key's scopes cover the required role level.
// Scope naming convention: "manifest:read" (auditor), "manifest:write" (operator), "manifest:admin" (admin).
// If the API key has no manifest-specific scopes, the check passes (role check handles authorization).
func hasSufficientScope(scopes []string, requiredRole auth.Role) bool {
	requiredLevel, ok := requiredScopeLevel[requiredRole]
	if !ok {
		return false
	}

	hasManifestScope := false
	for _, scope := range scopes {
		if level, exists := manifestScopeLevel[scope]; exists {
			hasManifestScope = true
			if level >= requiredLevel {
				return true
			}
		}
	}

	return !hasManifestScope
}
