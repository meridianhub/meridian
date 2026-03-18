// Package gateway provides HTTP middleware for API gateway functionality.
//
// [TenantResolverMiddleware] resolves the current tenant from the request
// subdomain and injects the tenant ID into the request context. In local
// development mode (LOCAL_DEV_MODE=true), the X-Tenant-Slug header may be
// used instead of subdomain resolution.
//
// # Usage
//
//	resolver, err := gateway.NewTenantResolverMiddleware(cache, repo, "api.meridian.io", logger)
//	http.Handle("/", resolver.Handler(appHandler))
package gateway
