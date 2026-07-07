package main

import (
	"context"
	"strings"

	"github.com/meridianhub/meridian/shared/platform/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// controlPlaneMethodPrefix is the gRPC namespace whose RPCs are governed by
// manifest RBAC. Only methods under this prefix have identity claims injected;
// all other services are left untouched (the unified binary continues to handle
// their auth at the gateway edge).
const controlPlaneMethodPrefix = "/meridian.control_plane.v1."

// Gateway-propagated identity metadata keys. These are the lowercase gRPC forms
// of the API gateway's X-User-ID / X-Auth-Roles / X-Tenant-ID headers. The
// gateway validates the caller's JWT at the HTTP edge, strips any client-supplied
// identity headers to prevent spoofing, and re-emits these from the validated
// claims (see services/api-gateway/metadata.go). By the time a request reaches
// the loopback gRPC server, x-auth-roles reflects verified roles.
const (
	mdKeyUserID    = "x-user-id"
	mdKeyAuthRoles = "x-auth-roles"
	mdKeyTenantID  = "x-tenant-id"
)

// GatewayManifestRBACUnaryInterceptor enforces manifest RBAC on the unified binary
// using the identity propagated by the API gateway.
//
// The unified binary cannot use the standard JWT auth interceptor globally: it
// serves platform-layer services (tenant, identity) that carry no tenant_id claim,
// runs on the demo with an in-process JWT signer (no external JWKS URL), and makes
// unauthenticated loopback self-calls. Instead, the gateway is the single JWT
// enforcement point; it forwards verified identity as trusted gRPC metadata.
//
// This interceptor bridges that model to the shared control-plane RBAC: for
// control-plane methods it reconstructs auth.Claims from the propagated metadata,
// then delegates to the control-plane ManifestRBAC interceptor. Callers with no
// propagated identity produce nil claims, so RBAC fails closed (PermissionDenied).
// Non-control-plane methods pass through untouched.
func GatewayManifestRBACUnaryInterceptor(rbac grpc.UnaryServerInterceptor) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		if !strings.HasPrefix(info.FullMethod, controlPlaneMethodPrefix) {
			return handler(ctx, req)
		}
		if claims := claimsFromGatewayMetadata(ctx); claims != nil {
			ctx = injectGatewayClaims(ctx, claims)
		}
		return rbac(ctx, req, info, handler)
	}
}

// GatewayManifestRBACStreamInterceptor is the streaming counterpart of
// GatewayManifestRBACUnaryInterceptor.
func GatewayManifestRBACStreamInterceptor(rbac grpc.StreamServerInterceptor) grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		if !strings.HasPrefix(info.FullMethod, controlPlaneMethodPrefix) {
			return handler(srv, ss)
		}
		if claims := claimsFromGatewayMetadata(ss.Context()); claims != nil {
			ss = &identityServerStream{ServerStream: ss, ctx: injectGatewayClaims(ss.Context(), claims)}
		}
		return rbac(srv, ss, info, handler)
	}
}

// claimsFromGatewayMetadata reconstructs auth.Claims from gateway-propagated
// identity metadata. It returns nil when no authenticated principal is present
// (no x-user-id), so downstream RBAC denies the request.
func claimsFromGatewayMetadata(ctx context.Context) *auth.Claims {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil
	}

	userID := firstMetadataValue(md, mdKeyUserID)
	if userID == "" {
		return nil
	}

	claims := &auth.Claims{
		UserID:   userID,
		TenantID: firstMetadataValue(md, mdKeyTenantID),
	}

	if rawRoles := firstMetadataValue(md, mdKeyAuthRoles); rawRoles != "" {
		for _, role := range strings.Split(rawRoles, ",") {
			if role = strings.TrimSpace(role); role != "" {
				claims.Roles = append(claims.Roles, role)
			}
		}
	}

	return claims
}

// injectGatewayClaims stores the reconstructed claims in the context using the
// same keys the auth interceptor uses, so auth.GetClaimsFromContext and the RBAC
// interceptor find them.
func injectGatewayClaims(ctx context.Context, claims *auth.Claims) context.Context {
	ctx = context.WithValue(ctx, auth.ClaimsContextKey, claims)
	ctx = context.WithValue(ctx, auth.UserIDContextKey, claims.UserID)
	ctx = context.WithValue(ctx, auth.RolesContextKey, claims.Roles)
	return ctx
}

// firstMetadataValue returns the first value for key, or "" if absent.
func firstMetadataValue(md metadata.MD, key string) string {
	if vals := md.Get(key); len(vals) > 0 {
		return strings.TrimSpace(vals[0])
	}
	return ""
}

// identityServerStream overrides Context() to carry the claims-enriched context
// through a streaming RPC.
type identityServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *identityServerStream) Context() context.Context { return s.ctx }
