package service

import (
	"github.com/meridianhub/meridian/services/control-plane/internal/server"
	"google.golang.org/grpc"
)

// ManifestRBACUnaryInterceptor re-exports the control-plane manifest RBAC unary
// interceptor so consumers outside the control-plane module tree (notably the
// unified binary in cmd/meridian) can enforce the same role-based access control
// on control-plane RPCs. The interceptor logic and role map are owned by the
// internal/server package; this is a thin, dependency-free re-export.
func ManifestRBACUnaryInterceptor() grpc.UnaryServerInterceptor {
	return server.ManifestRBACUnaryInterceptor()
}

// ManifestRBACStreamInterceptor re-exports the control-plane manifest RBAC stream
// interceptor. See ManifestRBACUnaryInterceptor for rationale.
func ManifestRBACStreamInterceptor() grpc.StreamServerInterceptor {
	return server.ManifestRBACStreamInterceptor()
}
