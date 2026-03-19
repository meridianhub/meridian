package testdb

import (
	"context"
	"testing"

	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// DefaultTestTenantID is the standard tenant ID used in tests that don't need
// a specific tenant value. Using a consistent default reduces boilerplate and
// makes tests easier to read.
const DefaultTestTenantID = "test_tenant"

// ContextWithTenant returns a context.Background() with the given tenant ID
// injected. This is a convenience wrapper that eliminates the common
// test boilerplate of:
//
//	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("my_tenant"))
//
// Usage:
//
//	ctx := testdb.ContextWithTenant(t, "my_tenant")
func ContextWithTenant(t *testing.T, tenantID string) context.Context {
	t.Helper()
	return tenant.WithTenant(context.Background(), tenant.TenantID(tenantID))
}

// ContextWithDefaultTenant returns a context.Background() with the default
// test tenant ID ("test_tenant") injected. Use this when the specific tenant
// ID value doesn't matter to the test.
//
// Usage:
//
//	ctx := testdb.ContextWithDefaultTenant(t)
func ContextWithDefaultTenant(t *testing.T) context.Context {
	t.Helper()
	return tenant.WithTenant(context.Background(), tenant.TenantID(DefaultTestTenantID))
}
