package gateway

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Host-based tenant routing with LOCAL_DEV_MODE disabled.
//
// This is the contract seed-dev relies on when applying a manifest against a
// deployed gateway, and it is the one no CI job exercises end to end: E2E runs
// LOCAL_DEV_MODE=true, while develop and any production-shaped deployment run it
// false. A change that works only under the header path therefore passes every
// suite and fails on the first real deploy - which is exactly what happened.
//
// These tests pin both halves: the Host must resolve, and the header alone must
// not.

const (
	hostRoutingBaseDomain = "develop.meridianhub.cloud"
	hostRoutingSlug       = "volterra-energy"
	hostRoutingTenantID   = "volterra_energy"
)

// newHostRoutingResolver builds a resolver with LOCAL_DEV_MODE disabled and a
// tenant reachable by slug.
func newHostRoutingResolver(t *testing.T) *TenantResolverMiddleware {
	t.Helper()

	tid, err := tenant.NewTenantID(hostRoutingTenantID)
	require.NoError(t, err)

	slugCache := &MockSlugCache{}
	// Cache miss, so resolution falls through to the repository.
	slugCache.On("Get", mock.Anything, mock.Anything).Return(tenant.TenantID(""), "", nil).Maybe()
	slugCache.On("Set", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	repo := &MockTenantRepository{}
	repo.On("GetBySlug", mock.Anything, hostRoutingSlug).Return(&domain.Tenant{
		ID:          tid,
		Slug:        hostRoutingSlug,
		DisplayName: "Volterra Energy",
		Status:      domain.StatusActive,
	}, nil).Maybe()
	repo.On("GetBySlug", mock.Anything, mock.Anything).Return(nil, domain.ErrNotFound).Maybe()

	resolver, err := NewTenantResolverMiddleware(
		slugCache, repo, hostRoutingBaseDomain, slog.Default(),
		false, // LOCAL_DEV_MODE disabled - the deployed configuration
	)
	require.NoError(t, err)
	return resolver
}

// serveThroughResolver runs one request through the resolver and reports the
// status and whether the downstream handler was reached.
func serveThroughResolver(t *testing.T, resolver *TenantResolverMiddleware, req *http.Request) (int, bool) {
	t.Helper()

	reached := false
	handler := resolver.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Code, reached
}

func TestTenantResolver_HostResolvesTenantWithoutLocalDevMode(t *testing.T) {
	// seed-dev dials the gateway on localhost but sets Host to the tenant
	// subdomain. The connection address and the routed tenant are independent.
	req := httptest.NewRequest(http.MethodPost, "http://localhost:8090/v1/manifests/apply", nil)
	req.Host = hostRoutingSlug + "." + hostRoutingBaseDomain

	status, reached := serveThroughResolver(t, newHostRoutingResolver(t), req)

	assert.True(t, reached, "a request whose Host carries the tenant subdomain must resolve")
	assert.Equal(t, http.StatusOK, status)
}

func TestTenantResolver_HeaderAloneIsRejectedWithoutLocalDevMode(t *testing.T) {
	// The regression: sending only X-Tenant-Slug, which is what the first
	// version of the seed-dev gateway path did. Honored only under
	// LOCAL_DEV_MODE, so on develop it fell through to Host parsing and 404'd.
	req := httptest.NewRequest(http.MethodPost, "http://localhost:8090/v1/manifests/apply", nil)
	req.Host = "localhost:8090"
	req.Header.Set(TenantSlugHeader, hostRoutingSlug)

	status, reached := serveThroughResolver(t, newHostRoutingResolver(t), req)

	assert.False(t, reached, "the header must not resolve a tenant when local dev mode is off")
	assert.Equal(t, http.StatusNotFound, status,
		"this is the 404 Invalid subdomain that broke the develop deploy")
}

func TestTenantResolver_HostWinsWhenBothPresentWithoutLocalDevMode(t *testing.T) {
	// seed-dev sends both, so the combination must behave like the Host case.
	req := httptest.NewRequest(http.MethodPost, "http://localhost:8090/v1/manifests/apply", nil)
	req.Host = hostRoutingSlug + "." + hostRoutingBaseDomain
	req.Header.Set(TenantSlugHeader, hostRoutingSlug)

	status, reached := serveThroughResolver(t, newHostRoutingResolver(t), req)

	assert.True(t, reached, "sending both must resolve, since seed-dev sends both")
	assert.Equal(t, http.StatusOK, status)
}

func TestTenantResolver_HostOutsideBaseDomainIsRejected(t *testing.T) {
	// A subdomain of the wrong base domain must not resolve: extractSlug
	// requires the ".<baseDomain>" suffix, so a mismatched deployment
	// configuration fails closed rather than routing to an unintended tenant.
	req := httptest.NewRequest(http.MethodPost, "http://localhost:8090/v1/manifests/apply", nil)
	req.Host = hostRoutingSlug + ".example.com"

	status, reached := serveThroughResolver(t, newHostRoutingResolver(t), req)

	assert.False(t, reached)
	assert.Equal(t, http.StatusNotFound, status)
}
