// Package tools_test exercises addTool's rate-limit enforcement together
// with the real session.Manager. It lives in a separate (external) test
// package because session imports tools for ToolCategory — an internal
// (package tools) test could not import session without creating a cycle.
package tools_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/mcp-server/internal/session"
	"github.com/meridianhub/meridian/services/mcp-server/internal/tools"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// TestManager_EnforcesIsolatedLimitsAcrossTenants is the CTL-4 regression
// test end-to-end: a single session.Manager (as wired once per process in
// cmd/wire.go) backs every tool call, routed through the real addTool
// wrapper via tools.RegisterValidationTools — no synthetic test-only tool.
// A tenant that exhausts its quota must never throttle a different tenant
// sharing that same Manager.
func TestManager_EnforcesIsolatedLimitsAcrossTenants(t *testing.T) {
	limits := map[tools.ToolCategory]session.CategoryLimit{
		tools.CategorySimulate: {MaxRequests: 1, Window: time.Minute},
	}
	manager := session.NewManager(limits)
	defer manager.Close()

	srv := mcp.NewServer(&mcp.Implementation{Name: "ratelimit-integration-test", Version: "v0.0.1"}, nil)
	// meridian_cel_validate is CategorySimulate and needs no gRPC backend,
	// so it exercises the real addTool wrapper without extra fixtures.
	tools.RegisterValidationTools(srv)

	params := celValidateParams(t)

	sessionA := connectTenant(t, srv, manager, "tenant-a")
	sessionB := connectTenant(t, srv, manager, "tenant-b")

	resultA1, err := sessionA.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "meridian_cel_validate", Arguments: params,
	})
	require.NoError(t, err)
	assert.False(t, resultA1.IsError, "tenant-a's first call should succeed")

	resultA2, err := sessionA.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "meridian_cel_validate", Arguments: params,
	})
	require.NoError(t, err)
	assert.True(t, resultA2.IsError, "tenant-a's second call should be rate limited")

	resultB1, err := sessionB.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "meridian_cel_validate", Arguments: params,
	})
	require.NoError(t, err)
	assert.False(t, resultB1.IsError, "tenant-b must have its own independent quota")
}

// connectTenant connects a fresh in-memory client/server session pair to
// srv, attaching rl and tenantID to the server-side context the same way
// auth.BearerMiddleware attaches an authenticated tenant to each HTTP
// request's context in production (see tenant.WithTenant).
func connectTenant(t *testing.T, srv *mcp.Server, rl tools.RateLimiter, tenantID string) *mcp.ClientSession {
	t.Helper()
	ctx := tools.WithRateLimiter(context.Background(), rl)
	ctx = tenant.WithTenant(ctx, tenant.TenantID(tenantID))

	ct, st := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	require.NoError(t, err)
	t.Cleanup(func() { ss.Close() })

	c := mcp.NewClient(&mcp.Implementation{Name: "ratelimit-integration-test-client", Version: "v0.0.1"}, nil)
	cs, err := c.Connect(context.Background(), ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { cs.Close() })
	return cs
}

func celValidateParams(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	raw := json.RawMessage(`{"expression": "1 == 1", "environment": "validation"}`)
	require.NoError(t, json.Unmarshal(raw, &m))
	return m
}
