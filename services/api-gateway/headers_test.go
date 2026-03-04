package gateway

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// uuidPattern matches a standard UUID v4 format.
var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// TestHeaderPropagationMiddleware_RequestIDGeneration verifies that x-request-id
// is generated when missing from the request.
func TestHeaderPropagationMiddleware_RequestIDGeneration(t *testing.T) {
	var capturedHeaders http.Header

	handler := HeaderPropagationMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Verify x-request-id was generated
	requestID := capturedHeaders.Get("x-request-id")
	assert.NotEmpty(t, requestID, "x-request-id should be generated when missing")
	assert.True(t, uuidPattern.MatchString(requestID), "x-request-id should be a valid UUID, got: %s", requestID)
}

// TestHeaderPropagationMiddleware_RequestIDPreservation verifies that existing
// x-request-id is preserved and not overwritten.
func TestHeaderPropagationMiddleware_RequestIDPreservation(t *testing.T) {
	var capturedHeaders http.Header
	existingRequestID := "existing-request-id-12345"

	handler := HeaderPropagationMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("x-request-id", existingRequestID)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Verify existing x-request-id was preserved
	assert.Equal(t, existingRequestID, capturedHeaders.Get("x-request-id"),
		"existing x-request-id should be preserved")
}

// TestHeaderPropagationMiddleware_ForwardedForSingleIP verifies that x-forwarded-for
// is set correctly when no prior chain exists.
func TestHeaderPropagationMiddleware_ForwardedForSingleIP(t *testing.T) {
	var capturedHeaders http.Header

	handler := HeaderPropagationMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "192.168.1.100:12345"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Verify x-forwarded-for was set with client IP
	assert.Equal(t, "192.168.1.100", capturedHeaders.Get("x-forwarded-for"),
		"x-forwarded-for should be set to client IP")
}

// TestHeaderPropagationMiddleware_ForwardedForAppend verifies that x-forwarded-for
// is appended to (not overwritten) when an existing chain exists.
func TestHeaderPropagationMiddleware_ForwardedForAppend(t *testing.T) {
	var capturedHeaders http.Header

	handler := HeaderPropagationMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("x-forwarded-for", "10.0.0.1, 10.0.0.2")
	req.RemoteAddr = "192.168.1.100:12345"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Verify x-forwarded-for was appended with proper format
	assert.Equal(t, "10.0.0.1, 10.0.0.2, 192.168.1.100", capturedHeaders.Get("x-forwarded-for"),
		"x-forwarded-for should append client IP to existing chain")
}

// TestHeaderPropagationMiddleware_ForwardedForMultipleHops verifies that
// multiple hops are correctly tracked in x-forwarded-for.
func TestHeaderPropagationMiddleware_ForwardedForMultipleHops(t *testing.T) {
	tests := []struct {
		name           string
		existingChain  string
		remoteAddr     string
		expectedResult string
	}{
		{
			name:           "single existing IP",
			existingChain:  "10.0.0.1",
			remoteAddr:     "192.168.1.1:8080",
			expectedResult: "10.0.0.1, 192.168.1.1",
		},
		{
			name:           "multiple existing IPs",
			existingChain:  "203.0.113.1, 198.51.100.1, 192.0.2.1",
			remoteAddr:     "10.0.0.1:443",
			expectedResult: "203.0.113.1, 198.51.100.1, 192.0.2.1, 10.0.0.1",
		},
		{
			name:           "IPv6 in chain",
			existingChain:  "2001:db8::1",
			remoteAddr:     "[2001:db8::2]:8080",
			expectedResult: "2001:db8::1, 2001:db8::2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedHeaders http.Header

			handler := HeaderPropagationMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				capturedHeaders = r.Header.Clone()
			}))

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.Header.Set("x-forwarded-for", tt.existingChain)
			req.RemoteAddr = tt.remoteAddr
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assert.Equal(t, tt.expectedResult, capturedHeaders.Get("x-forwarded-for"))
		})
	}
}

// TestHeaderPropagationMiddleware_ForwardedHost verifies that x-forwarded-host
// is set correctly from the Host header.
func TestHeaderPropagationMiddleware_ForwardedHost(t *testing.T) {
	var capturedHeaders http.Header

	handler := HeaderPropagationMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Host = "acme.api.meridian.io"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Verify x-forwarded-host was set
	assert.Equal(t, "acme.api.meridian.io", capturedHeaders.Get("x-forwarded-host"),
		"x-forwarded-host should be set to original Host header")
}

// TestHeaderPropagationMiddleware_ForwardedHostPreservation verifies that existing
// x-forwarded-host is preserved and not overwritten.
func TestHeaderPropagationMiddleware_ForwardedHostPreservation(t *testing.T) {
	var capturedHeaders http.Header
	existingForwardedHost := "original-host.example.com"

	handler := HeaderPropagationMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Host = "new-host.example.com"
	req.Header.Set("x-forwarded-host", existingForwardedHost)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Verify existing x-forwarded-host was preserved
	assert.Equal(t, existingForwardedHost, capturedHeaders.Get("x-forwarded-host"),
		"existing x-forwarded-host should be preserved")
}

// TestHeaderPropagationMiddleware_TenantIDPassthrough verifies that x-tenant-id
// is passed through unchanged (set by TenantResolverMiddleware).
func TestHeaderPropagationMiddleware_TenantIDPassthrough(t *testing.T) {
	var capturedHeaders http.Header
	tenantID := "acme_corp"

	handler := HeaderPropagationMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("x-tenant-id", tenantID)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Verify x-tenant-id was passed through
	assert.Equal(t, tenantID, capturedHeaders.Get("x-tenant-id"),
		"x-tenant-id should be passed through unchanged")
}

// TestHeaderPropagationMiddleware_XRealIPPriority verifies that X-Real-IP
// is used in preference to RemoteAddr for x-forwarded-for.
func TestHeaderPropagationMiddleware_XRealIPPriority(t *testing.T) {
	var capturedHeaders http.Header

	handler := HeaderPropagationMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Real-IP", "203.0.113.50") // Set by ingress/load balancer
	req.RemoteAddr = "10.0.0.1:12345"           // Internal proxy IP
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Verify X-Real-IP was used over RemoteAddr
	assert.Equal(t, "203.0.113.50", capturedHeaders.Get("x-forwarded-for"),
		"x-forwarded-for should use X-Real-IP when available")
}

// TestHeaderPropagationMiddleware_AllHeadersSet verifies that all expected
// headers are set in a typical request scenario.
func TestHeaderPropagationMiddleware_AllHeadersSet(t *testing.T) {
	var capturedHeaders http.Header

	handler := HeaderPropagationMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/party/create", nil)
	req.Host = "acme.api.meridian.io"
	req.RemoteAddr = "192.168.1.100:45678"
	req.Header.Set("x-tenant-id", "acme_corp") // Pre-set by TenantResolverMiddleware
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Verify all headers are set correctly
	assert.Equal(t, "acme_corp", capturedHeaders.Get("x-tenant-id"),
		"x-tenant-id should be passed through")
	assert.True(t, uuidPattern.MatchString(capturedHeaders.Get("x-request-id")),
		"x-request-id should be a valid UUID")
	assert.Equal(t, "192.168.1.100", capturedHeaders.Get("x-forwarded-for"),
		"x-forwarded-for should be set")
	assert.Equal(t, "acme.api.meridian.io", capturedHeaders.Get("x-forwarded-host"),
		"x-forwarded-host should be set")
}

// TestGetClientIP verifies the client IP extraction logic.
func TestGetClientIP(t *testing.T) {
	tests := []struct {
		name       string
		xRealIP    string
		remoteAddr string
		expected   string
	}{
		{
			name:       "X-Real-IP present",
			xRealIP:    "203.0.113.50",
			remoteAddr: "10.0.0.1:12345",
			expected:   "203.0.113.50",
		},
		{
			name:       "X-Real-IP empty, use RemoteAddr with port",
			xRealIP:    "",
			remoteAddr: "192.168.1.100:54321",
			expected:   "192.168.1.100",
		},
		{
			name:       "X-Real-IP empty, RemoteAddr without port",
			xRealIP:    "",
			remoteAddr: "192.168.1.100",
			expected:   "192.168.1.100",
		},
		{
			name:       "IPv6 RemoteAddr with port",
			xRealIP:    "",
			remoteAddr: "[2001:db8::1]:8080",
			expected:   "2001:db8::1",
		},
		{
			name:       "IPv6 X-Real-IP",
			xRealIP:    "2001:db8::1",
			remoteAddr: "[::1]:8080",
			expected:   "2001:db8::1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			if tt.xRealIP != "" {
				req.Header.Set("X-Real-IP", tt.xRealIP)
			}
			req.RemoteAddr = tt.remoteAddr

			result := getClientIP(req)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestHeaderPropagationMiddleware_EmptyRemoteAddr verifies behavior when
// RemoteAddr is empty or invalid.
func TestHeaderPropagationMiddleware_EmptyRemoteAddr(t *testing.T) {
	var capturedHeaders http.Header

	handler := HeaderPropagationMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = "" // Edge case: empty RemoteAddr
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Verify middleware doesn't crash and x-forwarded-for remains unset
	assert.Empty(t, capturedHeaders.Get("x-forwarded-for"),
		"x-forwarded-for should be empty when no client IP available")
}

// TestHeaderPropagationMiddleware_MiddlewareChainIntegration simulates
// the full middleware chain behavior.
func TestHeaderPropagationMiddleware_MiddlewareChainIntegration(t *testing.T) {
	var capturedHeaders http.Header

	// Simulate TenantResolverMiddleware by pre-setting x-tenant-id
	tenantMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Header.Set("x-tenant-id", "resolved_tenant")
			next.ServeHTTP(w, r)
		})
	}

	// Create middleware chain: TenantResolver -> HeaderPropagation -> Handler
	finalHandler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
	})

	chain := tenantMiddleware(HeaderPropagationMiddleware(finalHandler))

	req := httptest.NewRequest(http.MethodPost, "/v1/test", nil)
	req.Host = "tenant1.api.meridian.io"
	req.RemoteAddr = "203.0.113.100:12345"
	rec := httptest.NewRecorder()

	chain.ServeHTTP(rec, req)

	// Verify all headers are correctly set through the chain
	assert.Equal(t, "resolved_tenant", capturedHeaders.Get("x-tenant-id"))
	require.NotEmpty(t, capturedHeaders.Get("x-request-id"))
	assert.True(t, uuidPattern.MatchString(capturedHeaders.Get("x-request-id")))
	assert.Equal(t, "203.0.113.100", capturedHeaders.Get("x-forwarded-for"))
	assert.Equal(t, "tenant1.api.meridian.io", capturedHeaders.Get("x-forwarded-host"))
}
