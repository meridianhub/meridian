// Package tenant provides tenant identity types and context propagation helpers.
//
// [TenantID] is a strongly-typed identifier validated on construction.
// [WithTenant] and [TenantFromContext] carry the tenant ID through request contexts.
// [WithSlug] and [SlugFromContext] do the same for the URL-safe slug.
//
// The universal keys [TenantIDKey] ("x-tenant-id") and [TenantSlugKey] ("x-tenant-slug")
// are shared across all transport layers: JWT claims, context values, Kafka headers,
// gRPC metadata, and HTTP headers.
package tenant
