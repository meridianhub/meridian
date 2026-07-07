package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// RateLimiter abstracts per-key, per-category request throttling. It lives in
// the tools package — rather than session, which already imports tools for
// ToolCategory — purely to avoid an import cycle. session.Manager satisfies
// this interface.
type RateLimiter interface {
	// Allow reports whether the caller scoped to key has remaining capacity
	// for category. Each key (tenant or session) gets an independent quota,
	// so one caller's traffic can never exhaust another caller's limit.
	Allow(key string, category ToolCategory) bool
}

// rateLimiterCtxKey is the context key under which a RateLimiter is stored.
type rateLimiterCtxKey struct{}

// WithRateLimiter returns a context carrying rl. addTool consults it (via
// rateLimiterFromContext) to enforce per-tenant/session limits on every
// registered tool call, without threading rl through each Register*
// function's signature. A context with no RateLimiter attached — or a nil
// rl — disables enforcement.
func WithRateLimiter(ctx context.Context, rl RateLimiter) context.Context {
	return context.WithValue(ctx, rateLimiterCtxKey{}, rl)
}

// rateLimiterFromContext returns the RateLimiter attached to ctx, or nil if
// none was attached.
func rateLimiterFromContext(ctx context.Context) RateLimiter {
	rl, _ := ctx.Value(rateLimiterCtxKey{}).(RateLimiter)
	return rl
}

// rateLimitKey derives the scope a rate limit check is keyed on:
//   - the authenticated tenant, when present — injected into ctx by
//     auth.BearerMiddleware via tenant.WithTenant on the HTTP/OAuth
//     transport (see resolveTenantID for the equivalent pattern used by
//     tenant-scoped tools);
//   - otherwise the MCP session ID (stdio / API-key transport, or HTTP
//     without OAuth), so distinct client connections never share a quota;
//   - a shared fallback key only when neither is available.
//
// Keying on tenant first (rather than session) means multiple sessions
// authenticated for the same tenant correctly share one quota, while
// sessions for different tenants never interfere with each other.
func rateLimitKey(ctx context.Context, sess *mcp.ServerSession) string {
	if t, ok := tenant.FromContext(ctx); ok && !t.IsEmpty() {
		return "tenant:" + string(t)
	}
	if sess != nil {
		if id := sess.ID(); id != "" {
			return "session:" + id
		}
	}
	return "default"
}
