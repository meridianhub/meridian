package auth

import (
	"context"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// MethodPermission defines the required permission for a gRPC method.
// Use either AllowedRoles for direct role checks, or ResourceType+Permission
// for permission-based checks via the existing RBAC permission matrix.
// Set Public to true for pre-authentication endpoints (e.g., login, password reset)
// that must be accessible without a JWT.
type MethodPermission struct {
	ResourceType ResourceType
	Permission   Permission
	AllowedRoles []Role
	Public       bool
}

// MethodRBACConfig maps gRPC full method names to required permissions.
// Fail-closed: methods NOT in the map are DENIED by default.
type MethodRBACConfig struct {
	Permissions   map[string]MethodPermission
	AllowUnmapped bool // NOT RECOMMENDED -- for gradual rollout only
}

// AuditFunc is called for each RBAC decision with the method, user ID, and decision.
type AuditFunc func(method, userID, decision string)

// NewMethodRBACInterceptor creates a unary interceptor that enforces method-level RBAC.
// Methods not in the permission map are denied (fail-closed).
func NewMethodRBACInterceptor(cfg MethodRBACConfig) grpc.UnaryServerInterceptor {
	return newMethodRBACInterceptor(cfg, nil)
}

// NewMethodRBACInterceptorWithAudit creates a unary interceptor with an audit callback
// that is invoked for every RBAC decision (allowed or denied).
func NewMethodRBACInterceptorWithAudit(cfg MethodRBACConfig, audit AuditFunc) grpc.UnaryServerInterceptor {
	return newMethodRBACInterceptor(cfg, audit)
}

func newMethodRBACInterceptor(cfg MethodRBACConfig, audit AuditFunc) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		perm, mapped := cfg.Permissions[info.FullMethod]

		// Fail-closed: unmapped methods are denied unless AllowUnmapped is set
		if !mapped {
			if cfg.AllowUnmapped {
				if audit != nil {
					userID := ""
					if claims, ok := GetClaimsFromContext(ctx); ok {
						userID = claims.EffectiveUserID()
					}
					audit(info.FullMethod, userID, "allowed_unmapped")
				}
				return handler(ctx, req)
			}
			slog.Warn("RBAC denied unmapped method",
				"method", info.FullMethod,
			)
			if audit != nil {
				userID := ""
				if claims, ok := GetClaimsFromContext(ctx); ok {
					userID = claims.EffectiveUserID()
				}
				audit(info.FullMethod, userID, "denied_unmapped")
			}
			return nil, status.Errorf(codes.PermissionDenied,
				"method %s is not configured in RBAC policy", info.FullMethod)
		}

		// Fail closed on misconfiguration: Public + AllowedRoles is contradictory.
		// A method cannot be both public (no auth) and role-restricted simultaneously.
		if perm.Public && len(perm.AllowedRoles) > 0 {
			slog.Error("RBAC misconfiguration: method is both Public and has AllowedRoles",
				"method", info.FullMethod,
			)
			if audit != nil {
				userID := ""
				if claims, ok := GetClaimsFromContext(ctx); ok {
					userID = claims.EffectiveUserID()
				}
				audit(info.FullMethod, userID, "denied_misconfigured")
			}
			return nil, status.Errorf(codes.Internal,
				"RBAC misconfiguration for method %s: Public and AllowedRoles are mutually exclusive", info.FullMethod)
		}

		// Public methods bypass authentication entirely (e.g., login, password reset).
		if perm.Public {
			if audit != nil {
				userID := ""
				if claims, ok := GetClaimsFromContext(ctx); ok {
					userID = claims.EffectiveUserID()
				}
				audit(info.FullMethod, userID, "allowed_public")
			}
			return handler(ctx, req)
		}

		claims, ok := GetClaimsFromContext(ctx)
		if !ok {
			if audit != nil {
				audit(info.FullMethod, "", "denied")
			}
			return nil, status.Error(codes.Unauthenticated, "missing authentication context")
		}

		userID := claims.EffectiveUserID()

		if err := checkMethodPermission(claims, perm); err != nil {
			if audit != nil {
				audit(info.FullMethod, userID, "denied")
			}
			return nil, err
		}

		if audit != nil {
			audit(info.FullMethod, userID, "allowed")
		}

		return handler(ctx, req)
	}
}

// checkMethodPermission checks whether claims satisfy the method permission.
// It supports two modes:
//   - AllowedRoles: direct role membership check
//   - ResourceType+Permission: delegates to the existing RBAC permission matrix
func checkMethodPermission(claims *Claims, perm MethodPermission) error {
	// If AllowedRoles is specified, use direct role check
	if len(perm.AllowedRoles) > 0 {
		if HasAnyRole(claims, perm.AllowedRoles...) {
			return nil
		}
		return status.Error(codes.PermissionDenied, "insufficient role for method")
	}

	// Otherwise, use permission-based check via the RBAC matrix
	if perm.ResourceType != "" && perm.Permission != "" {
		if HasPermission(claims, perm.ResourceType, perm.Permission) {
			return nil
		}
		return status.Errorf(codes.PermissionDenied,
			"insufficient permission: requires %s on %s", perm.Permission, perm.ResourceType)
	}

	// No authorization rule defined -- deny (fail-closed)
	return status.Error(codes.PermissionDenied, "no authorization rule defined for method")
}
