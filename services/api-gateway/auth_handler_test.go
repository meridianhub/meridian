package gateway_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gateway "github.com/meridianhub/meridian/services/api-gateway"
	"github.com/meridianhub/meridian/services/identity/connector"
	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// decodeJWTPayload extracts the raw claims map from a JWT token string.
func decodeJWTPayload(t *testing.T, token string) map[string]interface{} {
	t.Helper()
	parts := strings.Split(token, ".")
	require.Len(t, parts, 3, "JWT must have 3 parts")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	var claims map[string]interface{}
	require.NoError(t, json.Unmarshal(payload, &claims))
	return claims
}

// stubConnector is a test implementation of connector.PasswordConnector.
type stubConnector struct {
	loginFn func(ctx context.Context, scopes []string, username, password string) (connector.Identity, bool, error)
}

func (s *stubConnector) Login(ctx context.Context, scopes []string, username, password string) (connector.Identity, bool, error) {
	return s.loginFn(ctx, scopes, username, password)
}

func newTestSigner(t *testing.T) *platformauth.JWTSigner {
	t.Helper()
	signer, err := platformauth.NewJWTSigner(platformauth.JWTSignerConfig{
		KeyID:  "test-1",
		Issuer: "test-meridian",
	})
	require.NoError(t, err)
	return signer
}

func TestAuthHandler_LoginSuccess(t *testing.T) {
	signer := newTestSigner(t)

	conn := &stubConnector{
		loginFn: func(_ context.Context, _ []string, username, password string) (connector.Identity, bool, error) {
			if username == "user@example.com" && password == "correct" {
				return connector.Identity{
					UserID:   "user-123",
					Username: "Test User",
					Email:    "user@example.com",
					Groups:   []string{"operator"},
				}, true, nil
			}
			return connector.Identity{}, false, nil
		},
	}

	handler, err := gateway.NewAuthHandler(gateway.AuthHandlerConfig{
		Connector: conn,
		Signer:    signer,
		Logger:    slog.Default(),
	})
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]string{
		"email":    "user@example.com",
		"password": "correct",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	// Inject tenant context (normally done by tenant resolver middleware).
	// Use distinct ID and slug to verify the JWT carries both correctly.
	tid, _ := tenant.NewTenantID("volterra_energy")
	ctx := tenant.WithTenant(req.Context(), tid)
	ctx = tenant.WithSlug(ctx, "volterra-energy")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	handler.HandleLogin(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]interface{}
	err = json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.NotEmpty(t, resp["access_token"])
	assert.Equal(t, "Bearer", resp["token_type"])
	assert.Equal(t, float64(3600), resp["expires_in"])

	// Verify the token is valid
	validator, err := platformauth.NewJWTValidator(signer.PublicKey())
	require.NoError(t, err)

	claims, err := validator.ValidateToken(resp["access_token"].(string))
	require.NoError(t, err)
	assert.Equal(t, "user@example.com", claims.Email)
	assert.Equal(t, "volterra_energy", claims.TenantID)
	assert.Equal(t, []string{"operator"}, claims.Roles)

	// Verify x-tenant-slug is included with the slug value (not the tenant ID).
	rawClaims := decodeJWTPayload(t, resp["access_token"].(string))
	assert.Equal(t, "volterra-energy", rawClaims["x-tenant-slug"],
		"JWT should carry x-tenant-slug with the URL-safe slug, not the tenant ID")
	assert.Equal(t, "volterra_energy", rawClaims["x-tenant-id"],
		"JWT should carry x-tenant-id with the internal tenant ID")
}

func TestAuthHandler_InvalidCredentials(t *testing.T) {
	signer := newTestSigner(t)

	conn := &stubConnector{
		loginFn: func(_ context.Context, _ []string, _, _ string) (connector.Identity, bool, error) {
			return connector.Identity{}, false, nil
		},
	}

	handler, err := gateway.NewAuthHandler(gateway.AuthHandlerConfig{
		Connector: conn,
		Signer:    signer,
		Logger:    slog.Default(),
	})
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]string{
		"email":    "wrong@example.com",
		"password": "wrong",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	tid, _ := tenant.NewTenantID("volterra")
	req = req.WithContext(tenant.WithTenant(req.Context(), tid))

	rec := httptest.NewRecorder()
	handler.HandleLogin(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	var resp map[string]string
	err = json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "invalid email or password", resp["error"])
}

func TestAuthHandler_MissingFields(t *testing.T) {
	signer := newTestSigner(t)

	handler, err := gateway.NewAuthHandler(gateway.AuthHandlerConfig{
		Connector: &stubConnector{},
		Signer:    signer,
		Logger:    slog.Default(),
	})
	require.NoError(t, err)

	tests := []struct {
		name string
		body map[string]string
	}{
		{"missing email", map[string]string{"password": "test"}},
		{"missing password", map[string]string{"email": "test@example.com"}},
		{"both empty", map[string]string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.body)
			req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			tid, _ := tenant.NewTenantID("test")
			req = req.WithContext(tenant.WithTenant(req.Context(), tid))

			rec := httptest.NewRecorder()
			handler.HandleLogin(rec, req)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

func TestAuthHandler_NoTenant(t *testing.T) {
	signer := newTestSigner(t)

	handler, err := gateway.NewAuthHandler(gateway.AuthHandlerConfig{
		Connector: &stubConnector{},
		Signer:    signer,
		Logger:    slog.Default(),
	})
	require.NoError(t, err)

	body, _ := json.Marshal(map[string]string{
		"email":    "test@example.com",
		"password": "test",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// No tenant context

	rec := httptest.NewRecorder()
	handler.HandleLogin(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAuthHandler_MethodNotAllowed(t *testing.T) {
	handler, err := gateway.NewAuthHandler(gateway.AuthHandlerConfig{
		Connector: &stubConnector{},
		Signer:    newTestSigner(t),
		Logger:    slog.Default(),
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/login", nil)
	rec := httptest.NewRecorder()
	handler.HandleLogin(rec, req)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}
