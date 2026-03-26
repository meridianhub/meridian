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
