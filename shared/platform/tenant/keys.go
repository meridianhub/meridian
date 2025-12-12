package tenant

// TenantIDKey is the universal key for tenant ID across all transport layers:
// JWT claims, context values, Kafka headers, gRPC metadata, HTTP headers.
//
// BREAKING CHANGE: Renamed from "x-org-id" to "x-tenant-id" to disambiguate
// from BIAN Party.Organization domain concept.
const TenantIDKey = "x-tenant-id"
