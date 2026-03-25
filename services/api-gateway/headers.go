package gateway

import (
	"net"
	"net/http"
	"strings"

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
		// CRITICAL: Appending maintains the client IP chain for proper request tracing.
		// Always use RemoteAddr here (the direct connection IP), not getClientIP,
		// because getClientIP reads proxy headers which would create circular references.
		if connectIP := remoteAddrIP(r); connectIP != "" {
			existing := r.Header.Get("x-forwarded-for")
			if existing != "" {
				r.Header.Set("x-forwarded-for", existing+", "+connectIP)
			} else {
				r.Header.Set("x-forwarded-for", connectIP)
			}
		}

		// 4. x-forwarded-host - original Host header (only if not already set)
		if r.Header.Get("x-forwarded-host") == "" {
			r.Header.Set("x-forwarded-host", r.Host)
		}

		next.ServeHTTP(w, r)
	})
}

// remoteAddrIP extracts the IP from RemoteAddr only (direct connection).
// Used by HeaderPropagationMiddleware to build the X-Forwarded-For chain.
func remoteAddrIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// getClientIP extracts the client IP address from the request.
// Priority: X-Real-IP > CF-Connecting-IP > X-Forwarded-For (first entry) > RemoteAddr.
//
// SECURITY: This function trusts proxy headers unconditionally. This is safe because:
//   - The gateway MUST run behind a trusted reverse proxy (Caddy, nginx-ingress, Envoy)
//   - The proxy MUST set X-Real-IP or forward CF-Connecting-IP from upstream (Cloudflare)
//   - The gateway MUST NOT be directly exposed to untrusted clients
//
// If these conditions are not met, attackers could spoof headers to bypass
// IP-based rate limiting or access controls in downstream services.
func getClientIP(r *http.Request) string {
	// Trust X-Real-IP from reverse proxy (set by Caddy/nginx from upstream headers)
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	// Cloudflare sets CF-Connecting-IP to the actual client IP
	if ip := r.Header.Get("CF-Connecting-IP"); ip != "" {
		return ip
	}
	// Standard proxy header: first entry is the original client
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if ip, _, ok := strings.Cut(xff, ","); ok {
			return strings.TrimSpace(ip)
		}
		return strings.TrimSpace(xff)
	}
	// Fallback to RemoteAddr (direct connection IP)
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}
