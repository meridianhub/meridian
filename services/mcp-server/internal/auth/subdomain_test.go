package auth

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExtractSubdomain(t *testing.T) {
	tests := []struct {
		name       string
		host       string
		baseDomain string
		want       string
	}{
		{
			name:       "simple subdomain",
			host:       "acme.demo.meridianhub.cloud",
			baseDomain: "demo.meridianhub.cloud",
			want:       "acme",
		},
		{
			name:       "subdomain with port",
			host:       "acme.demo.meridianhub.cloud:8090",
			baseDomain: "demo.meridianhub.cloud",
			want:       "acme",
		},
		{
			name:       "no subdomain",
			host:       "demo.meridianhub.cloud",
			baseDomain: "demo.meridianhub.cloud",
			want:       "",
		},
		{
			name:       "wrong domain",
			host:       "acme.other.com",
			baseDomain: "demo.meridianhub.cloud",
			want:       "",
		},
		{
			name:       "localhost",
			host:       "localhost:8090",
			baseDomain: "demo.meridianhub.cloud",
			want:       "",
		},
		{
			name:       "127.0.0.1",
			host:       "127.0.0.1:8090",
			baseDomain: "demo.meridianhub.cloud",
			want:       "",
		},
		{
			name:       "empty host",
			host:       "",
			baseDomain: "demo.meridianhub.cloud",
			want:       "",
		},
		{
			name:       "empty base domain",
			host:       "acme.demo.meridianhub.cloud",
			baseDomain: "",
			want:       "",
		},
		{
			name:       "multi-level subdomain",
			host:       "volterra-energy.demo.meridianhub.cloud",
			baseDomain: "demo.meridianhub.cloud",
			want:       "volterra-energy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractSubdomain(tt.host, tt.baseDomain)
			if got != tt.want {
				t.Errorf("extractSubdomain(%q, %q) = %q, want %q", tt.host, tt.baseDomain, got, tt.want)
			}
		})
	}
}

// mockClaimsValidator implements ClaimsBearerValidator for testing.
type mockClaimsValidator struct {
	tenantID string
	err      error
}

func (m *mockClaimsValidator) ValidateBearer(_ string) error {
	return m.err
}

func (m *mockClaimsValidator) ValidateBearerWithTenant(_ string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.tenantID, nil
}

func TestIsBaseDomainAccess(t *testing.T) {
	t.Run("returns true when context has baseDomainAccessKey=true", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), baseDomainAccessKey, true)
		if !IsBaseDomainAccess(ctx) {
			t.Error("expected IsBaseDomainAccess to return true")
		}
	})

	t.Run("returns false when context lacks the key", func(t *testing.T) {
		ctx := context.Background()
		if IsBaseDomainAccess(ctx) {
			t.Error("expected IsBaseDomainAccess to return false")
		}
	})

	t.Run("returns false when context has key set to false", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), baseDomainAccessKey, false)
		if IsBaseDomainAccess(ctx) {
			t.Error("expected IsBaseDomainAccess to return false")
		}
	})
}

func TestTenantSubdomainMiddleware(t *testing.T) {
	logger := slog.Default()
	meta := Metadata{
		AuthorizationURL: "http://localhost/oauth/authorize",
		TokenURL:         "http://localhost/oauth/token",
	}

	okHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	t.Run("skips validation when baseDomain is empty", func(t *testing.T) {
		mw := NewTenantSubdomainMiddleware("", logger)
		validator := &mockClaimsValidator{tenantID: "acme"}

		handler := mw.Handler(validator, meta, okHandler)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/mcp", nil)
		req.Host = "acme.demo.meridianhub.cloud"
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("allows request without subdomain", func(t *testing.T) {
		mw := NewTenantSubdomainMiddleware("demo.meridianhub.cloud", logger)
		validator := &mockClaimsValidator{tenantID: "acme"}

		handler := mw.Handler(validator, meta, okHandler)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/mcp", nil)
		req.Host = "demo.meridianhub.cloud"
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("allows matching subdomain and JWT tenant", func(t *testing.T) {
		mw := NewTenantSubdomainMiddleware("demo.meridianhub.cloud", logger)
		validator := &mockClaimsValidator{tenantID: "volterra-energy"}

		handler := mw.Handler(validator, meta, okHandler)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/mcp", nil)
		req.Host = "volterra-energy.demo.meridianhub.cloud"
		req.Header.Set("Authorization", "Bearer valid-token")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("rejects mismatching subdomain and JWT tenant", func(t *testing.T) {
		mw := NewTenantSubdomainMiddleware("demo.meridianhub.cloud", logger)
		validator := &mockClaimsValidator{tenantID: "other-tenant"}

		handler := mw.Handler(validator, meta, okHandler)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/mcp", nil)
		req.Host = "volterra-energy.demo.meridianhub.cloud"
		req.Header.Set("Authorization", "Bearer valid-token")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("expected 403, got %d", rec.Code)
		}
	})

	t.Run("rejects request with subdomain but no bearer token", func(t *testing.T) {
		mw := NewTenantSubdomainMiddleware("demo.meridianhub.cloud", logger)
		validator := &mockClaimsValidator{tenantID: "acme"}

		handler := mw.Handler(validator, meta, okHandler)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/mcp", nil)
		req.Host = "acme.demo.meridianhub.cloud"
		// No Authorization header
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("rejects request with invalid bearer token", func(t *testing.T) {
		mw := NewTenantSubdomainMiddleware("demo.meridianhub.cloud", logger)
		validator := &mockClaimsValidator{err: ErrInvalidBearerToken}

		handler := mw.Handler(validator, meta, okHandler)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/mcp", nil)
		req.Host = "acme.demo.meridianhub.cloud"
		req.Header.Set("Authorization", "Bearer bad-token")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("annotates context with baseDomainAccessKey when no subdomain present", func(t *testing.T) {
		mw := NewTenantSubdomainMiddleware("demo.meridianhub.cloud", logger)
		validator := &mockClaimsValidator{tenantID: "acme"}

		var capturedCtx context.Context
		captureHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedCtx = r.Context()
			w.WriteHeader(http.StatusOK)
		})

		handler := mw.Handler(validator, meta, captureHandler)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/mcp", nil)
		req.Host = "demo.meridianhub.cloud"
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rec.Code)
		}
		if !IsBaseDomainAccess(capturedCtx) {
			t.Error("expected IsBaseDomainAccess to be true for base domain request")
		}
	})

	t.Run("does not annotate context with baseDomainAccessKey when subdomain present", func(t *testing.T) {
		mw := NewTenantSubdomainMiddleware("demo.meridianhub.cloud", logger)
		validator := &mockClaimsValidator{tenantID: "acme"}

		var capturedCtx context.Context
		captureHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedCtx = r.Context()
			w.WriteHeader(http.StatusOK)
		})

		handler := mw.Handler(validator, meta, captureHandler)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/mcp", nil)
		req.Host = "acme.demo.meridianhub.cloud"
		req.Header.Set("Authorization", "Bearer valid-token")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rec.Code)
		}
		if IsBaseDomainAccess(capturedCtx) {
			t.Error("expected IsBaseDomainAccess to be false for subdomain request")
		}
	})
}
