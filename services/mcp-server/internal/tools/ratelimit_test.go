package tools

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// --- rateLimitKey ---

func TestRateLimitKey_TenantPresent_ReturnsTenantScopedKey(t *testing.T) {
	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("acme"))
	assert.Equal(t, "tenant:acme", rateLimitKey(ctx, nil))
}

func TestRateLimitKey_NoTenantNoSession_ReturnsDefault(t *testing.T) {
	assert.Equal(t, "default", rateLimitKey(context.Background(), nil))
}

func TestRateLimitKey_NoTenantEmptySessionID_ReturnsDefault(t *testing.T) {
	// A ServerSession with no underlying connection has an empty ID, and
	// should fall back to the shared default key rather than an empty string.
	assert.Equal(t, "default", rateLimitKey(context.Background(), &mcp.ServerSession{}))
}

func TestRateLimitKey_TenantTakesPrecedenceOverSession(t *testing.T) {
	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("acme"))
	// Even with a (non-resolving) session present, an authenticated tenant wins.
	assert.Equal(t, "tenant:acme", rateLimitKey(ctx, &mcp.ServerSession{}))
}

// --- addTool rate limit enforcement ---

// fakeRateLimiter is a minimal per-key counter used to verify that addTool
// enforces whatever RateLimiter is attached to the request context. It
// deliberately avoids depending on the session package (which imports tools,
// so tools cannot import session back) — session.Manager is exercised
// end-to-end in ratelimit_integration_test.go instead.
type fakeRateLimiter struct {
	mu     sync.Mutex
	counts map[string]int
	max    int
}

func newFakeRateLimiter(maxRequests int) *fakeRateLimiter {
	return &fakeRateLimiter{counts: make(map[string]int), max: maxRequests}
}

func (f *fakeRateLimiter) Allow(key string, _ ToolCategory) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts[key]++
	return f.counts[key] <= f.max
}

// connectSession connects a fresh in-memory client/server session pair to
// srv, with rl (and optionally a tenant) attached to the server-side context
// so addTool can enforce it. tenantID may be empty to omit tenant scoping.
//
// The in-memory transport's connection always reports an empty session ID
// (see mcp.ioConn.SessionID — only the streamable HTTP transport's connection
// assigns real ones), so rateLimitKey's session-ID fallback cannot be
// exercised through this harness; tenant scoping is used instead to prove
// per-caller isolation. This mirrors the documented limitation in
// tenant_enforcement_test.go.
func connectSession(t *testing.T, srv *mcp.Server, rl RateLimiter, tenantID string) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	if rl != nil {
		ctx = WithRateLimiter(ctx, rl)
	}
	if tenantID != "" {
		ctx = tenant.WithTenant(ctx, tenant.TenantID(tenantID))
	}

	ct, st := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	require.NoError(t, err)
	t.Cleanup(func() { ss.Close() })

	c := mcp.NewClient(&mcp.Implementation{Name: "ratelimit-test-client", Version: "v0.0.1"}, nil)
	cs, err := c.Connect(context.Background(), ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { cs.Close() })
	return cs
}

func registerLimitedTool(srv *mcp.Server, name string) {
	addTool(srv, Tool{
		Name:        name,
		Description: "A rate-limited tool",
		InputSchema: emptySchema,
		Category:    CategoryWrite,
		Handler: func(_ context.Context, _ json.RawMessage) (interface{}, error) {
			return "ok", nil
		},
	})
}

func TestAddTool_NoRateLimiterInContext_NeverBlocks(t *testing.T) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "ratelimit-test", Version: "v0.0.1"}, nil)
	registerLimitedTool(srv, "limited")
	cs := connectSession(t, srv, nil, "")

	for i := 0; i < 10; i++ {
		result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "limited"})
		require.NoError(t, err)
		assert.False(t, result.IsError, "call %d should not be rate limited when no limiter is attached", i+1)
	}
}

func TestAddTool_RateLimiterInContext_BlocksAfterLimit(t *testing.T) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "ratelimit-test", Version: "v0.0.1"}, nil)
	registerLimitedTool(srv, "limited")
	rl := newFakeRateLimiter(1)
	cs := connectSession(t, srv, rl, "")

	result1, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "limited"})
	require.NoError(t, err)
	assert.False(t, result1.IsError, "first call should succeed")

	result2, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "limited"})
	require.NoError(t, err)
	assert.True(t, result2.IsError, "second call should be rate limited")
}

// TestAddTool_RateLimiting_IsolatesPerTenant is the CTL-4 regression test:
// two callers share the same RateLimiter instance (as they would in the real
// server, where one Manager backs every connection), but because
// rateLimitKey scopes on the authenticated tenant, one tenant exhausting its
// quota must never throttle a different tenant.
func TestAddTool_RateLimiting_IsolatesPerTenant(t *testing.T) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "ratelimit-test", Version: "v0.0.1"}, nil)
	registerLimitedTool(srv, "limited")
	rl := newFakeRateLimiter(1)

	sessionA := connectSession(t, srv, rl, "tenant-a")
	sessionB := connectSession(t, srv, rl, "tenant-b")

	resultA1, err := sessionA.CallTool(context.Background(), &mcp.CallToolParams{Name: "limited"})
	require.NoError(t, err)
	assert.False(t, resultA1.IsError, "session A's first call should succeed")

	resultA2, err := sessionA.CallTool(context.Background(), &mcp.CallToolParams{Name: "limited"})
	require.NoError(t, err)
	assert.True(t, resultA2.IsError, "tenant-a's second call should be rate limited")

	resultB1, err := sessionB.CallTool(context.Background(), &mcp.CallToolParams{Name: "limited"})
	require.NoError(t, err)
	assert.False(t, resultB1.IsError, "tenant-b must be unaffected by tenant-a's exhausted limit")
}
