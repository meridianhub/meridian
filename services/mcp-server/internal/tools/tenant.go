package tools

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// resolveTenantID reconciles a client-supplied tenant_id against the tenant
// established by the authenticated request context, returning the tenant that
// tool handlers must scope their backend calls to.
//
// When the request context carries an authenticated tenant — the OAuth/JWT HTTP
// transport injects it via auth.BearerMiddleware from the token's tenant claim —
// that tenant is authoritative:
//   - a supplied tenant_id that differs is rejected with codes.PermissionDenied,
//     preventing a caller authenticated for tenant A from reaching tenant B's data;
//   - a supplied tenant_id that matches, or an omitted tenant_id, resolves to the
//     authenticated tenant.
//
// When the context carries no authenticated tenant — the stdio / API-key
// transport, where the api-gateway resolves the tenant from the bearer key —
// the supplied tenant_id is returned unchanged and the gateway remains the
// enforcement point.
//
// The error deliberately omits the supplied tenant_id so a caller cannot probe
// for the existence of other tenants.
func resolveTenantID(ctx context.Context, requested string) (string, error) {
	authTenant, ok := tenant.FromContext(ctx)
	if !ok || authTenant.IsEmpty() {
		return requested, nil
	}
	authID := string(authTenant)
	if requested != "" && requested != authID {
		return "", status.Error(codes.PermissionDenied,
			"tenant_id does not match the authenticated tenant")
	}
	return authID, nil
}
