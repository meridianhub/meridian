// Package auth provides authentication and authorization utilities.
// This file contains Prometheus metrics for security monitoring.
package auth

import (
	"context"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

// tenantMismatchTotal tracks tenant context mismatch detection events.
// This is a SECURITY metric - alerts should be configured for non-zero values.
//
// A tenant mismatch occurs when a user's JWT contains one tenant_id but they
// attempt to access resources on a different tenant's subdomain. This could indicate:
// - A user accidentally accessing the wrong subdomain
// - A potential cross-tenant access attack
// - A misconfiguration in the gateway's tenant resolution
//
// Labels:
//   - jwt_tenant: The tenant_id from the JWT claims
//   - header_tenant: The tenant_id from the x-tenant-id gRPC metadata header
var tenantMismatchTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "meridian",
	Subsystem: "auth",
	Name:      "tenant_mismatch_total",
	Help:      "Total number of tenant context mismatch detection events (security metric)",
}, []string{"jwt_tenant", "header_tenant"})

// RecordTenantMismatch increments the tenant mismatch counter.
// This should be called when the JWT tenant_id does not match the header tenant_id.
func RecordTenantMismatch(jwtTenant, headerTenant string) {
	tenantMismatchTotal.WithLabelValues(jwtTenant, headerTenant).Inc()
}

// extractClientIP extracts the client IP address from gRPC context.
// It checks standard headers in order of preference:
//  1. x-forwarded-for (first IP in the chain, set by proxies/load balancers)
//  2. x-real-ip (set by nginx and other reverse proxies)
//  3. peer address (direct gRPC connection)
//
// Returns empty string if no client IP can be determined.
func extractClientIP(ctx context.Context) string {
	if ctx == nil {
		return ""
	}

	// Check metadata for forwarded IP (common in Kubernetes/Istio environments)
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		// x-forwarded-for may contain multiple IPs (client, proxy1, proxy2...)
		// First IP is the original client
		if vals := md.Get("x-forwarded-for"); len(vals) > 0 && vals[0] != "" {
			// Split on comma and take first IP
			if ips := strings.Split(vals[0], ","); len(ips) > 0 {
				return strings.TrimSpace(ips[0])
			}
		}
		// Fallback to x-real-ip header
		if vals := md.Get("x-real-ip"); len(vals) > 0 && vals[0] != "" {
			return vals[0]
		}
	}

	// Fallback to peer address (direct gRPC connection)
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		// Extract just the IP portion (peer address may include port like "192.168.1.1:50051")
		addr := p.Addr.String()
		if idx := strings.LastIndex(addr, ":"); idx > 0 {
			return addr[:idx]
		}
		return addr
	}

	return ""
}
