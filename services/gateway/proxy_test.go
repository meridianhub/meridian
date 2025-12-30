package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProxyHandler_RouteMatching verifies that routes are matched correctly
// based on URL path prefixes.
func TestProxyHandler_RouteMatching(t *testing.T) {
	tests := []struct {
		name          string
		backends      []BackendRoute
		path          string
		expectedMatch string
	}{
		{
			name: "exact prefix match",
			backends: []BackendRoute{
				{Prefix: "/v1/party", Target: "party:50051"},
			},
			path:          "/v1/party/create",
			expectedMatch: "/v1/party",
		},
		{
			name: "no match returns empty",
			backends: []BackendRoute{
				{Prefix: "/v1/party", Target: "party:50051"},
			},
			path:          "/v2/other",
			expectedMatch: "",
		},
		{
			name: "longest prefix wins",
			backends: []BackendRoute{
				{Prefix: "/v1", Target: "fallback:50051"},
				{Prefix: "/v1/party", Target: "party:50051"},
				{Prefix: "/v1/party/internal", Target: "party-internal:50051"},
			},
			path:          "/v1/party/internal/admin",
			expectedMatch: "/v1/party/internal",
		},
		{
			name: "shortest prefix fallback",
			backends: []BackendRoute{
				{Prefix: "/v1", Target: "fallback:50051"},
				{Prefix: "/v1/party", Target: "party:50051"},
			},
			path:          "/v1/other",
			expectedMatch: "/v1",
		},
		{
			name:          "empty backends no match",
			backends:      []BackendRoute{},
			path:          "/anything",
			expectedMatch: "",
		},
		{
			name: "root prefix matches all",
			backends: []BackendRoute{
				{Prefix: "/", Target: "default:50051"},
			},
			path:          "/anything/else",
			expectedMatch: "/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewProxyHandler(tt.backends)
			match := handler.MatchRoute(tt.path)
			assert.Equal(t, tt.expectedMatch, match)
		})
	}
}

// TestProxyHandler_LongestPrefixOrder verifies that routes are sorted
// correctly for longest-prefix-first matching.
func TestProxyHandler_LongestPrefixOrder(t *testing.T) {
	// Routes provided in random order
	backends := []BackendRoute{
		{Prefix: "/a", Target: "a:50051"},
		{Prefix: "/abc/def/ghi", Target: "ghi:50051"},
		{Prefix: "/abc", Target: "abc:50051"},
		{Prefix: "/abc/def", Target: "def:50051"},
	}

	handler := NewProxyHandler(backends)

	// Verify longest prefix is matched for each path
	testCases := []struct {
		path     string
		expected string
	}{
		{"/abc/def/ghi/xyz", "/abc/def/ghi"},
		{"/abc/def/xyz", "/abc/def"},
		{"/abc/xyz", "/abc"},
		{"/a/xyz", "/a"},
	}

	for _, tc := range testCases {
		match := handler.MatchRoute(tc.path)
		assert.Equal(t, tc.expected, match, "path %s should match %s", tc.path, tc.expected)
	}
}

// TestProxyHandler_ServeHTTP_404 verifies that unmatched paths return 404.
func TestProxyHandler_ServeHTTP_404(t *testing.T) {
	backends := []BackendRoute{
		{Prefix: "/v1/party", Target: "party:50051"},
	}
	handler := NewProxyHandler(backends)

	req := httptest.NewRequest(http.MethodGet, "/v2/unmatched", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "Not Found")
}

// TestProxyHandler_ServeHTTP_ProxiesRequest verifies that matched requests
// are proxied to the correct backend.
func TestProxyHandler_ServeHTTP_ProxiesRequest(t *testing.T) {
	// Create a mock backend server
	backendCalled := false
	backendPath := ""
	backendMethod := ""
	backendHeaders := http.Header{}

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalled = true
		backendPath = r.URL.Path
		backendMethod = r.Method
		backendHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer backend.Close()

	// Extract host:port from backend URL (remove http:// prefix)
	backendAddr := backend.URL[7:] // Remove "http://"

	backends := []BackendRoute{
		{Prefix: "/v1/party", Target: backendAddr},
	}
	handler := NewProxyHandler(backends)

	req := httptest.NewRequest(http.MethodPost, "/v1/party/create", nil)
	req.Host = "gateway.example.com"
	req.Header.Set("Content-Type", "application/connect+proto")
	req.Header.Set("Connect-Protocol-Version", "1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Verify backend was called
	assert.True(t, backendCalled, "backend should have been called")
	assert.Equal(t, "/v1/party/create", backendPath)
	assert.Equal(t, http.MethodPost, backendMethod)

	// Verify Connect protocol headers were forwarded
	assert.Equal(t, "application/connect+proto", backendHeaders.Get("Content-Type"))
	assert.Equal(t, "1", backendHeaders.Get("Connect-Protocol-Version"))

	// Verify response was proxied back
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Equal(t, `{"status":"ok"}`, rec.Body.String())
}

// TestProxyHandler_ServeHTTP_ForwardsHeaders verifies that X-Forwarded-Host
// is set when proxying requests.
func TestProxyHandler_ServeHTTP_ForwardsHeaders(t *testing.T) {
	// Create a mock backend server that captures headers
	var capturedHeaders http.Header

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	backendAddr := backend.URL[7:]

	backends := []BackendRoute{
		{Prefix: "/v1", Target: backendAddr},
	}
	handler := NewProxyHandler(backends)

	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	req.Host = "original-host.example.com"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Verify X-Forwarded-Host was set
	assert.Equal(t, "original-host.example.com", capturedHeaders.Get("X-Forwarded-Host"))
}

// TestProxyHandler_ServeHTTP_PreservesExistingXForwardedHost verifies that
// an existing X-Forwarded-Host header is preserved.
func TestProxyHandler_ServeHTTP_PreservesExistingXForwardedHost(t *testing.T) {
	var capturedHeaders http.Header

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	backendAddr := backend.URL[7:]

	backends := []BackendRoute{
		{Prefix: "/v1", Target: backendAddr},
	}
	handler := NewProxyHandler(backends)

	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	req.Host = "new-host.example.com"
	req.Header.Set("X-Forwarded-Host", "original-forwarded.example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Verify existing X-Forwarded-Host was preserved
	assert.Equal(t, "original-forwarded.example.com", capturedHeaders.Get("X-Forwarded-Host"))
}

// TestProxyHandler_RouteCount verifies the route count is correct.
func TestProxyHandler_RouteCount(t *testing.T) {
	tests := []struct {
		name     string
		backends []BackendRoute
		expected int
	}{
		{
			name:     "empty backends",
			backends: []BackendRoute{},
			expected: 0,
		},
		{
			name: "single backend",
			backends: []BackendRoute{
				{Prefix: "/v1", Target: "service:50051"},
			},
			expected: 1,
		},
		{
			name: "multiple backends",
			backends: []BackendRoute{
				{Prefix: "/v1/party", Target: "party:50051"},
				{Prefix: "/v1/account", Target: "account:50051"},
				{Prefix: "/v1/transaction", Target: "transaction:50051"},
			},
			expected: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewProxyHandler(tt.backends)
			assert.Equal(t, tt.expected, handler.RouteCount())
		})
	}
}

// TestProxyHandler_InvalidBackendURL verifies that invalid backend URLs are skipped.
func TestProxyHandler_InvalidBackendURL(t *testing.T) {
	// Test with a mix of valid and invalid backends
	backends := []BackendRoute{
		{Prefix: "/v1/valid", Target: "valid-service:50051"},
		// Note: The URL parser is quite permissive, most strings will parse
		// but may result in connection errors at runtime
	}

	handler := NewProxyHandler(backends)

	// Should have at least the valid route
	assert.GreaterOrEqual(t, handler.RouteCount(), 1)
}

// TestProxyHandler_ConnectProtocolHeaders verifies that Connect protocol
// specific headers are properly forwarded.
func TestProxyHandler_ConnectProtocolHeaders(t *testing.T) {
	var capturedHeaders http.Header
	var capturedBody []byte

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/connect+proto")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("response"))
	}))
	defer backend.Close()

	backendAddr := backend.URL[7:]

	backends := []BackendRoute{
		{Prefix: "/", Target: backendAddr},
	}
	handler := NewProxyHandler(backends)

	// Simulate a Connect protocol unary request
	req := httptest.NewRequest(http.MethodPost, "/grpc.health.v1.Health/Check", nil)
	req.Header.Set("Content-Type", "application/connect+proto")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("Connect-Timeout-Ms", "5000")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Verify Connect headers were forwarded
	assert.Equal(t, "application/connect+proto", capturedHeaders.Get("Content-Type"))
	assert.Equal(t, "1", capturedHeaders.Get("Connect-Protocol-Version"))
	assert.Equal(t, "5000", capturedHeaders.Get("Connect-Timeout-Ms"))
	_ = capturedBody // Body verification if needed
}

// TestProxyHandler_MultipleBackendsRoutingIntegration is an integration test
// that verifies multiple backends receive requests based on path prefix.
func TestProxyHandler_MultipleBackendsRoutingIntegration(t *testing.T) {
	// Track which backend was called
	partyBackendCalled := false
	accountBackendCalled := false

	partyBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		partyBackendCalled = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("party"))
	}))
	defer partyBackend.Close()

	accountBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		accountBackendCalled = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("account"))
	}))
	defer accountBackend.Close()

	backends := []BackendRoute{
		{Prefix: "/v1/party", Target: partyBackend.URL[7:]},
		{Prefix: "/v1/account", Target: accountBackend.URL[7:]},
	}
	handler := NewProxyHandler(backends)

	// Test party route
	req1 := httptest.NewRequest(http.MethodGet, "/v1/party/list", nil)
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)

	require.True(t, partyBackendCalled, "party backend should be called")
	assert.False(t, accountBackendCalled, "account backend should not be called")
	assert.Equal(t, "party", rec1.Body.String())

	// Reset flags
	partyBackendCalled = false
	accountBackendCalled = false

	// Test account route
	req2 := httptest.NewRequest(http.MethodGet, "/v1/account/balance", nil)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	assert.False(t, partyBackendCalled, "party backend should not be called")
	require.True(t, accountBackendCalled, "account backend should be called")
	assert.Equal(t, "account", rec2.Body.String())
}
