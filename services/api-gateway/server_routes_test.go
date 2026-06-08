package gateway

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/meridianhub/meridian/services/api-gateway/auth"
	"github.com/meridianhub/meridian/services/api-gateway/eventstream"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newRouteTestConfig returns a minimal Config for route registration tests.
func newRouteTestConfig() *Config {
	return &Config{
		Port:        8080,
		BaseDomain:  "api.example.com",
		DatabaseURL: "postgres://localhost/test",
	}
}

// newLegacyAPIKeyAuthMiddleware builds a CombinedAuthMiddleware backed by a
// legacy API key map. This is the lightest-weight way to produce a non-nil
// auth middleware so that auth-gated routes (admin, MCP consent, event stream)
// are registered with the full chain. Requests without credentials are rejected
// with 401 before reaching the wrapped handler.
func newLegacyAPIKeyAuthMiddleware(t *testing.T) *auth.CombinedAuthMiddleware {
	t.Helper()
	mw, err := auth.NewCombinedAuthMiddleware(auth.CombinedAuthConfig{
		APIKeyConfig: auth.APIKeyConfig{
			APIKeys: map[string]string{"test-key": "test-identity"},
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	require.NoError(t, err)
	t.Cleanup(mw.Close)
	return mw
}

// TestHandler_ReturnsMux verifies the Handler accessor exposes the server mux
// so integration tests can drive it via httptest.
func TestHandler_ReturnsMux(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(newRouteTestConfig(), logger, nil)

	h := server.Handler()
	require.NotNil(t, h)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

// TestHandleVersion_WithVersionInfo verifies the /version endpoint returns the
// configured build metadata as JSON when WithVersionInfo is supplied.
func TestHandleVersion_WithVersionInfo(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	info := &VersionInfo{Version: "1.2.3", Commit: "abc123", BuildDate: "2026-06-08"}
	server := NewServer(newRouteTestConfig(), logger, nil, WithVersionInfo(info))

	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	rec := httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")

	var got VersionInfo
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, *info, got)
}

// TestHandleVersion_DefaultsWhenUnset verifies the /version endpoint returns
// sensible "dev"/"unknown" defaults when no VersionInfo is configured.
func TestHandleVersion_DefaultsWhenUnset(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(newRouteTestConfig(), logger, nil)

	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	rec := httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got VersionInfo
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, "dev", got.Version)
	assert.Equal(t, "unknown", got.Commit)
	assert.Equal(t, "unknown", got.BuildDate)
}

// TestHandleVersion_NonGETReturns405 verifies the version endpoint is GET-only.
func TestHandleVersion_NonGETReturns405(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(newRouteTestConfig(), logger, nil, WithVersionInfo(&VersionInfo{Version: "x"}))

	req := httptest.NewRequest(http.MethodPost, "/version", nil)
	rec := httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Equal(t, "GET, HEAD", rec.Header().Get("Allow"))
}

// TestRegisterMCPOAuthRoutes_AllEndpointsRegistered verifies every populated
// MCPOAuthEndpoints handler is wired to its route, and that tenant resolution
// is applied to the authorize endpoint.
func TestRegisterMCPOAuthRoutes_AllEndpointsRegistered(t *testing.T) {
	const baseDomain = "api.example.com"
	tenantID := tenant.MustNewTenantID("acme_corp")
	resolver := newTestTenantResolver(t, baseDomain, "acme", tenantID)

	marker := func(name string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(name))
		})
	}

	var authorizeTenantFound bool
	authorize := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, authorizeTenantFound = tenant.FromContext(r.Context())
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("authorize"))
	})

	endpoints := &MCPOAuthEndpoints{
		Authorize:   authorize,
		Callback:    marker("callback"),
		Token:       marker("token"),
		ConsentInfo: marker("consent-info"),
		Metadata:    func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("metadata")) },
		Register:    marker("register"),
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	config := newRouteTestConfig()
	config.BaseDomain = baseDomain
	server := NewServer(config, logger, resolver, WithMCPOAuthEndpoints(endpoints))

	cases := []struct {
		method, path, want string
	}{
		{http.MethodGet, "/oauth/callback", "callback"},
		{http.MethodPost, "/oauth/token", "token"},
		{http.MethodGet, "/oauth/consent-info", "consent-info"},
		{http.MethodGet, "/.well-known/oauth-authorization-server", "metadata"},
		{http.MethodPost, "/oauth/register", "register"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, nil)
		rec := httptest.NewRecorder()
		server.mux.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code, c.path)
		assert.Equal(t, c.want, rec.Body.String(), c.path)
	}

	// Authorize goes through optional tenant resolution: a tenant subdomain
	// request must have tenant context injected.
	req := httptest.NewRequest(http.MethodGet, "/oauth/authorize", nil)
	req.Host = "acme." + baseDomain
	rec := httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, authorizeTenantFound, "tenant should be resolved from subdomain on /oauth/authorize")
}

// TestRegisterMCPOAuthRoutes_PartialEndpoints verifies that nil endpoint fields
// are skipped (no route registered) while populated ones are wired.
func TestRegisterMCPOAuthRoutes_PartialEndpoints(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	endpoints := &MCPOAuthEndpoints{
		Token: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("token"))
		}),
		// Authorize/Callback/ConsentInfo/Metadata/Register intentionally nil.
	}
	server := NewServer(newRouteTestConfig(), logger, nil, WithMCPOAuthEndpoints(endpoints))

	// Registered endpoint works.
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", nil)
	rec := httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Unregistered endpoint falls through to the "/" catch-all (handleAPI 503).
	req = httptest.NewRequest(http.MethodGet, "/oauth/callback", nil)
	rec = httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// TestRegisterTenantInfoRoute_WithResolver verifies the public tenant-info
// endpoint resolves tenant context from the subdomain and returns the slug.
func TestRegisterTenantInfoRoute_WithResolver(t *testing.T) {
	const baseDomain = "api.example.com"
	tenantID := tenant.MustNewTenantID("acme_corp")
	resolver := newTestTenantResolver(t, baseDomain, "acme", tenantID)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewTenantInfoHandler(logger)
	t.Cleanup(handler.Stop)

	config := newRouteTestConfig()
	config.BaseDomain = baseDomain
	server := NewServer(config, logger, resolver, WithTenantInfoHandler(handler))

	req := httptest.NewRequest(http.MethodGet, "/api/tenant-info", nil)
	req.Host = "acme." + baseDomain
	rec := httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "acme", body["slug"])
}

// TestRegisterTenantInfoRoute_WithoutResolver verifies the tenant-info endpoint
// is still registered (and reachable) when no tenant resolver is configured;
// without resolved tenant context it returns 404.
func TestRegisterTenantInfoRoute_WithoutResolver(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewTenantInfoHandler(logger)
	t.Cleanup(handler.Stop)

	server := NewServer(newRouteTestConfig(), logger, nil, WithTenantInfoHandler(handler))

	req := httptest.NewRequest(http.MethodGet, "/api/tenant-info", nil)
	rec := httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestRegisterAdminRoutes_RegisteredWithAuth verifies the admin verify route is
// registered when both an admin handler and auth middleware are configured, and
// that an unauthenticated request is rejected by the auth chain (401).
func TestRegisterAdminRoutes_RegisteredWithAuth(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	authMW := newLegacyAPIKeyAuthMiddleware(t)

	// Set the admin handler field directly: NewAdminHandler requires a non-nil
	// identity repository, but the route only needs adminHandler != nil to be
	// registered. An unauthenticated request is rejected before the handler
	// (and thus the repo) is ever touched.
	setAdmin := func(s *Server) { s.adminHandler = &AdminHandler{logger: logger} }

	server := NewServer(newRouteTestConfig(), logger, nil,
		WithAuthMiddleware(authMW), setAdmin)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/identities/abc/verify", nil)
	rec := httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code,
		"admin route must be registered and gated by the auth chain")
}

// TestRegisterAdminRoutes_SkippedWithoutAuth verifies the admin route is NOT
// registered when auth middleware is absent (it logs an error and returns).
// The request then falls through to the "/" catch-all (503).
func TestRegisterAdminRoutes_SkippedWithoutAuth(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	setAdmin := func(s *Server) { s.adminHandler = &AdminHandler{logger: logger} }

	server := NewServer(newRouteTestConfig(), logger, nil, setAdmin)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/identities/abc/verify", nil)
	rec := httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code,
		"admin route must not be registered without auth; request hits the fallback handler")
}

// TestWrapWithAuthChain_FullChainRejectsUnauthenticated verifies that when an
// auth middleware is configured, NewServer builds the tenant-authz middleware
// and the full chain rejects credential-less API requests with 401.
func TestWrapWithAuthChain_FullChainRejectsUnauthenticated(t *testing.T) {
	const baseDomain = "api.example.com"
	tenantID := tenant.MustNewTenantID("acme_corp")
	resolver := newTestTenantResolver(t, baseDomain, "acme", tenantID)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	authMW := newLegacyAPIKeyAuthMiddleware(t)

	config := newRouteTestConfig()
	config.BaseDomain = baseDomain
	server := NewServer(config, logger, resolver, WithAuthMiddleware(authMW))

	require.NotNil(t, server.tenantAuthzMiddleware,
		"tenant authz middleware must be created when auth middleware is configured")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/anything", nil)
	req.Host = "acme." + baseDomain
	rec := httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestRegisterEventStreamRoute_TypedHandler verifies that WithEventStreamHandler
// (the production typed handler path) registers GET /ws/events through
// buildEventStreamHandler, and that the claims bridge forwards to the
// eventstream handler (which rejects a no-claims request with 401).
func TestRegisterEventStreamRoute_TypedHandler(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Real eventstream handler; with no claims in context it returns 401 before
	// attempting any websocket upgrade, so nil source/fanOut are never used.
	esHandler := eventstream.NewHandler(eventstream.NewRouter(nil, nil), logger)

	// No auth middleware configured: wrapWithAuthChain passes the claims bridge
	// through unwrapped, so the request reaches the bridge and the eventstream
	// handler directly, exercising buildEventStreamHandler's closure.
	server := NewServer(newRouteTestConfig(), logger, nil, WithEventStreamHandler(esHandler))

	req := httptest.NewRequest(http.MethodGet, "/ws/events", nil)
	rec := httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code,
		"event stream handler rejects requests without auth claims")
}

// TestWrapWithTenantOnly_NoResolverPassesThrough verifies that Dex routes are
// registered and pass through unchanged when no tenant resolver is configured.
func TestWrapWithTenantOnly_NoResolverPassesThrough(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dex := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("dex-passthrough"))
	})

	server := NewServer(newRouteTestConfig(), logger, nil, WithDexHandler(dex))

	req := httptest.NewRequest(http.MethodGet, "/dex/auth", nil)
	rec := httptest.NewRecorder()
	server.mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "dex-passthrough", rec.Body.String())
}

// TestShutdown_StopsTenantInfoHandler verifies Shutdown stops the tenant info
// handler's background cleanup goroutine without error, even when the server was
// never started.
func TestShutdown_StopsTenantInfoHandler(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewTenantInfoHandler(logger)

	server := NewServer(newRouteTestConfig(), logger, nil, WithTenantInfoHandler(handler))

	// Shutdown without Start returns nil but still runs the cleanup branches.
	require.NoError(t, server.Shutdown(t.Context()))

	// Stop must be idempotent: the explicit cleanup helper below is safe to call
	// even though Shutdown already stopped the handler.
	handler.Stop()
}
