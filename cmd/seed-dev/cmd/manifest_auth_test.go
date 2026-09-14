package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadSeedAuth_ReportsConfigured(t *testing.T) {
	t.Setenv("PLATFORM_ADMIN_EMAIL", "admin@example.com")
	t.Setenv("PLATFORM_ADMIN_PASSWORD", "secret")

	auth := loadSeedAuth()

	assert.True(t, auth.configured())
	assert.Equal(t, "admin@example.com", auth.email)
}

func TestLoadSeedAuth_UnconfiguredWhenPasswordMissing(t *testing.T) {
	t.Setenv("PLATFORM_ADMIN_EMAIL", "admin@example.com")
	t.Setenv("PLATFORM_ADMIN_PASSWORD", "")

	assert.False(t, loadSeedAuth().configured())
}

func TestEnsureTenantAdmin_NoopWithoutCredentials(t *testing.T) {
	// No credentials means no database connection is attempted, so this stays a
	// no-op even with DATABASE_URL unset.
	t.Setenv("DATABASE_URL", "")

	require.NoError(t, ensureTenantAdmin(t.Context(), seedAuth{}, "dev_tenant"))
}

func TestLogin_ReturnsAccessTokenAndSendsTenantSlug(t *testing.T) {
	var gotSlug, gotContentType string
	var gotBody map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, loginPath, r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		gotSlug = r.Header.Get("X-Tenant-Slug")
		gotContentType = r.Header.Get("Content-Type")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(loginResponse{
			AccessToken: "test-token",
			TokenType:   "Bearer",
			ExpiresIn:   3600,
		})
	}))
	defer srv.Close()

	token, err := login(t.Context(), srv.Client(), srv.URL,
		seedAuth{email: "admin@example.com", password: "secret"}, "dev-tenant", "dev-tenant.localhost")

	require.NoError(t, err)
	assert.Equal(t, "test-token", token)
	assert.Equal(t, "dev-tenant", gotSlug, "tenant must be resolvable without a subdomain")
	assert.Equal(t, "application/json", gotContentType)
	assert.Equal(t, "admin@example.com", gotBody["email"])
	assert.Equal(t, "secret", gotBody["password"])
}

func TestLogin_ErrorsOnRejectedCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid credentials"}`))
	}))
	defer srv.Close()

	_, err := login(t.Context(), srv.Client(), srv.URL,
		seedAuth{email: "admin@example.com", password: "wrong"}, "dev-tenant", "dev-tenant.localhost")

	require.ErrorIs(t, err, ErrLoginFailed)
	assert.Contains(t, err.Error(), "invalid credentials")
}

func TestLogin_ErrorsWhenTokenAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"token_type":"Bearer"}`))
	}))
	defer srv.Close()

	_, err := login(t.Context(), srv.Client(), srv.URL,
		seedAuth{email: "admin@example.com", password: "secret"}, "dev-tenant", "dev-tenant.localhost")

	require.ErrorIs(t, err, ErrLoginFailed)
	assert.Contains(t, err.Error(), "no access token")
}

// writeMinimalManifest writes a manifest that parses as protobuf JSON.
func writeMinimalManifest(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manifest.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"version":"1"}`), 0o600))
	return path
}

func TestApplyManifestHTTP_SendsBearerTokenToTranscodedRoute(t *testing.T) {
	var gotAuth, gotPath, gotSlug, gotHost string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotSlug = r.Header.Get("X-Tenant-Slug")
		gotHost = r.Host

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jobId":"job-1","status":"APPLY_MANIFEST_STATUS_APPLIED"}`))
	}))
	defer srv.Close()

	err := applyManifestHTTP(t.Context(), srv.Client(), srv.URL,
		"dev-tenant", "dev-tenant.localhost", "test-token", writeMinimalManifest(t), false)

	require.NoError(t, err)
	assert.Equal(t, "Bearer test-token", gotAuth,
		"the gateway is the only place the token is verified, so it must be sent there")
	assert.Equal(t, manifestApplyPath, gotPath)
	assert.Equal(t, "dev-tenant", gotSlug)
	// Deployed gateways run LOCAL_DEV_MODE=false and ignore X-Tenant-Slug, so the
	// Host is what actually resolves the tenant there. Asserting it is the point:
	// sending only the header is what made this fail with 404 on develop.
	assert.Equal(t, "dev-tenant.localhost", gotHost)
}

func TestSetTenantRouting_OverridesHostIndependentlyOfDialAddress(t *testing.T) {
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://localhost:8090/x", nil)
	require.NoError(t, err)

	setTenantRouting(req, "volterra-energy", "volterra-energy.develop.meridianhub.cloud")

	assert.Equal(t, "volterra-energy.develop.meridianhub.cloud", req.Host,
		"the resolver must see the tenant subdomain while the connection stays on localhost")
	assert.Equal(t, "localhost:8090", req.URL.Host, "the dial address must be unchanged")
	assert.Equal(t, "volterra-energy", req.Header.Get("X-Tenant-Slug"))
}

func TestSetTenantRouting_LeavesHostAloneWhenUnknown(t *testing.T) {
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://localhost:8090/x", nil)
	require.NoError(t, err)

	before := req.Host

	setTenantRouting(req, "dev-tenant", "")

	assert.Equal(t, before, req.Host, "an unknown subdomain must not rewrite the Host")
	assert.Equal(t, "dev-tenant", req.Header.Get("X-Tenant-Slug"))
}

func TestApplyManifestHTTP_OmitsAuthorizationWhenTokenEmpty(t *testing.T) {
	var hadAuthHeader bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadAuthHeader = r.Header["Authorization"]
		_, _ = w.Write([]byte(`{"status":"APPLY_MANIFEST_STATUS_APPLIED"}`))
	}))
	defer srv.Close()

	err := applyManifestHTTP(t.Context(), srv.Client(), srv.URL,
		"dev-tenant", "dev-tenant.localhost", "", writeMinimalManifest(t), false)

	require.NoError(t, err)
	assert.False(t, hadAuthHeader, "no token means no header, not an empty bearer")
}

func TestApplyManifestHTTP_SurfacesUnauthenticated(t *testing.T) {
	// The failure this whole path exists to prevent: RBAC denying an unidentified
	// caller. It must surface as a clear error rather than a parse failure.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":"unauthenticated","message":"authentication required"}`))
	}))
	defer srv.Close()

	err := applyManifestHTTP(t.Context(), srv.Client(), srv.URL,
		"dev-tenant", "dev-tenant.localhost", "", writeMinimalManifest(t), false)

	require.ErrorIs(t, err, ErrApplyManifestHTTP)
	assert.Contains(t, err.Error(), "authentication required")
	assert.Contains(t, err.Error(), "PLATFORM_ADMIN_EMAIL",
		"a denial with no token must name the missing configuration")
}

func TestApplyManifestHTTP_DeniedWithTokenOmitsCredentialHint(t *testing.T) {
	// A denial while holding a token is a roles problem, not missing config.
	// Pointing at PLATFORM_ADMIN_* there would send the reader down a dead end.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"permission denied"}`))
	}))
	defer srv.Close()

	err := applyManifestHTTP(t.Context(), srv.Client(), srv.URL,
		"dev-tenant", "dev-tenant.localhost", "tok", writeMinimalManifest(t), false)

	require.ErrorIs(t, err, ErrApplyManifestHTTP)
	assert.NotContains(t, err.Error(), "PLATFORM_ADMIN_EMAIL")
}

func TestApplyManifestHTTP_ErrorsOnNonSuccessStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"APPLY_MANIFEST_STATUS_FAILED"}`))
	}))
	defer srv.Close()

	err := applyManifestHTTP(t.Context(), srv.Client(), srv.URL,
		"dev-tenant", "dev-tenant.localhost", "tok", writeMinimalManifest(t), false)

	require.ErrorIs(t, err, ErrManifestApplyFailed)
}

func TestApplyManifestHTTP_ErrorsOnValidationErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"APPLY_MANIFEST_STATUS_APPLIED",` +
			`"validationErrors":[{"path":"spec.x","message":"bad","severity":"ERROR"}]}`))
	}))
	defer srv.Close()

	err := applyManifestHTTP(t.Context(), srv.Client(), srv.URL,
		"dev-tenant", "dev-tenant.localhost", "tok", writeMinimalManifest(t), false)

	require.ErrorIs(t, err, ErrManifestValidation)
}

func TestApplyManifestHTTP_ToleratesUnknownResponseFields(t *testing.T) {
	// An additive proto change on the server must not fail the seed.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"APPLY_MANIFEST_STATUS_APPLIED","futureField":"whatever"}`))
	}))
	defer srv.Close()

	err := applyManifestHTTP(t.Context(), srv.Client(), srv.URL,
		"dev-tenant", "dev-tenant.localhost", "tok", writeMinimalManifest(t), false)

	require.NoError(t, err)
}

func TestNewSeedHTTPClient_AppliesTimeout(t *testing.T) {
	assert.Equal(t, 5*time.Second, newSeedHTTPClient(5*time.Second).Timeout)
}
