package gateway

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleTenantInfo_Success(t *testing.T) {
	handler := NewTenantInfoHandler(slog.Default())

	ctx := tenant.WithSlug(tenant.WithTenant(
		t.Context(), tenant.TenantID("acme_corp"),
	), "acme-corp")
	ctx = tenant.WithDisplayName(ctx, "Acme Corporation")

	req := httptest.NewRequest(http.MethodGet, "/api/tenant-info", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	handler.HandleTenantInfo().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "public, s-maxage=300", rec.Header().Get("Cache-Control"))

	var resp tenantInfoResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "acme-corp", resp.Slug)
	assert.Equal(t, "Acme Corporation", resp.DisplayName)
}

func TestHandleTenantInfo_NoTenantContext(t *testing.T) {
	handler := NewTenantInfoHandler(slog.Default())

	req := httptest.NewRequest(http.MethodGet, "/api/tenant-info", nil)
	rec := httptest.NewRecorder()

	handler.HandleTenantInfo().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandleTenantInfo_MethodNotAllowed(t *testing.T) {
	handler := NewTenantInfoHandler(slog.Default())

	req := httptest.NewRequest(http.MethodPost, "/api/tenant-info", nil)
	rec := httptest.NewRecorder()

	handler.HandleTenantInfo().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestRateLimitHandler_RejectsExcessRequests(t *testing.T) {
	handler := NewTenantInfoHandler(slog.Default())
	defer handler.Stop()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	wrapped := handler.RateLimitHandler(inner)

	// Burst of 10 should all succeed.
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.1:12345"
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code, "request %d should succeed", i)
	}

	// 11th request should be rate-limited.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
}

func TestRateLimitHandler_UsesRemoteAddr(t *testing.T) {
	handler := NewTenantInfoHandler(slog.Default())
	defer handler.Stop()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	wrapped := handler.RateLimitHandler(inner)

	// Exhaust burst for 10.0.0.1.
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.1:12345"
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
	}

	// Different RemoteAddr should still succeed (separate bucket).
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.2:12345"
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Spoofed X-Real-IP should NOT bypass the limit for 10.0.0.1.
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Real-IP", "10.0.0.99")
	rec = httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusTooManyRequests, rec.Code)
}

func TestHandleTenantInfo_EmptyDisplayName(t *testing.T) {
	handler := NewTenantInfoHandler(slog.Default())

	ctx := tenant.WithSlug(tenant.WithTenant(
		t.Context(), tenant.TenantID("acme_corp"),
	), "acme-corp")
	// No display name set

	req := httptest.NewRequest(http.MethodGet, "/api/tenant-info", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	handler.HandleTenantInfo().ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp tenantInfoResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "acme-corp", resp.Slug)
	assert.Equal(t, "", resp.DisplayName)
}
