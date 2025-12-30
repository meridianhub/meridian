package gateway

import (
	"net"
	"net/http"

	"github.com/google/uuid"
)

// HeaderPropagationMiddleware ensures critical headers are set for backend services.
// It handles:
// - x-request-id: Generated if missing (for distributed tracing)
// - x-forwarded-for: Client IP chain (appended, not overwritten)
// - x-forwarded-host: Original Host header
//
// Note: x-tenant-id is set by TenantResolverMiddleware and passed through unchanged.
func HeaderPropagationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. x-tenant-id already set by TenantResolverMiddleware - no action needed

		// 2. x-request-id - generate if missing (for distributed tracing)
		if r.Header.Get("x-request-id") == "" {
			r.Header.Set("x-request-id", uuid.New().String())
		}

		// 3. x-forwarded-for - APPEND to existing (don't overwrite)
		// CRITICAL: Appending maintains the client IP chain for proper request tracing
		// and prevents IP spoofing by preserving the original chain
		if clientIP := getClientIP(r); clientIP != "" {
			existing := r.Header.Get("x-forwarded-for")
			if existing != "" {
				r.Header.Set("x-forwarded-for", existing+", "+clientIP)
			} else {
				r.Header.Set("x-forwarded-for", clientIP)
			}
		}

		// 4. x-forwarded-host - original Host header (only if not already set)
		if r.Header.Get("x-forwarded-host") == "" {
			r.Header.Set("x-forwarded-host", r.Host)
		}

		next.ServeHTTP(w, r)
	})
}

// getClientIP extracts the client IP address from the request.
// It prioritizes X-Real-IP (typically set by ingress/load balancer) over RemoteAddr.
//
// SECURITY: This function trusts X-Real-IP unconditionally. This is safe because:
//   - The gateway MUST run behind a trusted ingress controller (nginx-ingress, Envoy)
//   - The ingress MUST set X-Real-IP from the actual client connection
//   - The gateway MUST NOT be directly exposed to untrusted clients
//   - Kubernetes NetworkPolicy should restrict direct access to the gateway pod
//
// If these conditions are not met, attackers could spoof X-Real-IP to bypass
// IP-based rate limiting or access controls in downstream services.
func getClientIP(r *http.Request) string {
	// Trust X-Real-IP from ingress (nginx-ingress, Envoy set this from the actual client)
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	// Fallback to RemoteAddr (direct connection IP)
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr might not have a port (e.g., Unix sockets)
		return r.RemoteAddr
	}
	return ip
}
