package testdb

import (
	"testing"

	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContextWithTenant(t *testing.T) {
	ctx := ContextWithTenant(t, "my_tenant")
	tid, ok := tenant.FromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, tenant.TenantID("my_tenant"), tid)
}

func TestContextWithDefaultTenant(t *testing.T) {
	ctx := ContextWithDefaultTenant(t)
	tid, ok := tenant.FromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, tenant.TenantID(DefaultTestTenantID), tid)
}
