package middleware_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/meridianhub/meridian/services/gateway/internal/middleware"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTenantResolver_ValidSubdomain(t *testing.T) {
	tests := []struct {
		name           string
		host           string
		expectedTenant string
	}{
		{
			name:           "simple tenant",
			host:           "acme.api.meridianhub.cloud",
			expectedTenant: "acme",
		},
		{
			name:           "tenant with numbers",
			host:           "bank123.api.meridianhub.cloud",
			expectedTenant: "bank123",
		},
		{
			name:           "tenant with underscore",
			host:           "acme_bank.api.meridianhub.cloud",
			expectedTenant: "acme_bank",
		},
		{
			name:           "uppercase tenant",
			host:           "ACME.api.meridianhub.cloud",
			expectedTenant: "ACME",
		},
		{
			name:           "mixed case tenant",
			host:           "AcmeBank.api.meridianhub.cloud",
			expectedTenant: "AcmeBank",
		},
		{
			name:           "host with port",
			host:           "acme.api.meridianhub.cloud:8080",
			expectedTenant: "acme",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := middleware.NewTenantResolver("api.meridianhub.cloud", nil)

			var capturedTenant tenant.TenantID
			var tenantFound bool

			handler := resolver.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				capturedTenant, tenantFound = tenant.FromContext(r.Context())
			}))

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.Host = tt.host
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusOK, rec.Code)
			assert.True(t, tenantFound, "tenant should be found in context")
			assert.Equal(t, tt.expectedTenant, capturedTenant.String())
		})
	}
}

func TestTenantResolver_MissingTenant(t *testing.T) {
	tests := []struct {
		name string
		host string
	}{
		{
			name: "base domain only",
			host: "api.meridianhub.cloud",
		},
		{
			name: "base domain with port",
			host: "api.meridianhub.cloud:8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := middleware.NewTenantResolver("api.meridianhub.cloud", nil)

			handler := resolver.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
				t.Error("handler should not be called when tenant is missing")
			}))

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.Host = tt.host
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			body, _ := io.ReadAll(rec.Body)
			assert.Contains(t, string(body), "tenant subdomain required")
		})
	}
}

func TestTenantResolver_InvalidHost(t *testing.T) {
	tests := []struct {
		name string
		host string
	}{
		{
			name: "different domain",
			host: "acme.example.com",
		},
		{
			name: "partial match",
			host: "acme.meridianhub.cloud",
		},
		{
			name: "different TLD",
			host: "acme.api.meridian.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := middleware.NewTenantResolver("api.meridianhub.cloud", nil)

			handler := resolver.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
				t.Error("handler should not be called for invalid host")
			}))

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.Host = tt.host
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			body, _ := io.ReadAll(rec.Body)
			assert.Contains(t, string(body), "invalid host")
		})
	}
}

func TestTenantResolver_InvalidTenantID(t *testing.T) {
	tests := []struct {
		name string
		host string
	}{
		{
			name: "tenant with hyphen",
			host: "acme-bank.api.meridianhub.cloud",
		},
		{
			name: "tenant with special chars",
			host: "acme@bank.api.meridianhub.cloud",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := middleware.NewTenantResolver("api.meridianhub.cloud", nil)

			handler := resolver.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
				t.Error("handler should not be called for invalid tenant ID")
			}))

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.Host = tt.host
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			body, _ := io.ReadAll(rec.Body)
			assert.Contains(t, string(body), "invalid tenant identifier")
		})
	}
}

func TestTenantResolver_AllowedHosts(t *testing.T) {
	tests := []struct {
		name         string
		host         string
		allowedHosts []string
		expectBypass bool
	}{
		{
			name:         "localhost bypassed",
			host:         "localhost",
			allowedHosts: []string{"localhost"},
			expectBypass: true,
		},
		{
			name:         "localhost with port bypassed",
			host:         "localhost:8080",
			allowedHosts: []string{"localhost"},
			expectBypass: true,
		},
		{
			name:         "127.0.0.1 bypassed",
			host:         "127.0.0.1:8080",
			allowedHosts: []string{"127.0.0.1"},
			expectBypass: true,
		},
		{
			name:         "non-allowed host requires tenant",
			host:         "api.meridianhub.cloud",
			allowedHosts: []string{"localhost"},
			expectBypass: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := middleware.NewTenantResolver("api.meridianhub.cloud", tt.allowedHosts)

			handlerCalled := false
			handler := resolver.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				handlerCalled = true
				// When bypassed, tenant should not be in context
				if tt.expectBypass {
					_, found := tenant.FromContext(r.Context())
					assert.False(t, found, "tenant should not be in context for bypassed hosts")
				}
			}))

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.Host = tt.host
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if tt.expectBypass {
				assert.True(t, handlerCalled, "handler should be called for bypassed hosts")
				assert.Equal(t, http.StatusOK, rec.Code)
			} else {
				assert.False(t, handlerCalled, "handler should not be called for non-bypassed hosts requiring tenant")
				assert.Equal(t, http.StatusBadRequest, rec.Code)
			}
		})
	}
}

func TestTenantResolver_SetsHeader(t *testing.T) {
	resolver := middleware.NewTenantResolver("api.meridianhub.cloud", nil)

	var capturedHeader string
	handler := resolver.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedHeader = r.Header.Get(tenant.TenantIDKey)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Host = "acme.api.meridianhub.cloud"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "acme", capturedHeader)
}

func TestTenantResolver_NestedSubdomain(t *testing.T) {
	// When there are multiple subdomains, only the first one is used as tenant
	resolver := middleware.NewTenantResolver("api.meridianhub.cloud", nil)

	var capturedTenant tenant.TenantID
	handler := resolver.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedTenant, _ = tenant.FromContext(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Host = "team.acme.api.meridianhub.cloud"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	// Takes the first subdomain segment
	assert.Equal(t, "team", capturedTenant.String())
}
