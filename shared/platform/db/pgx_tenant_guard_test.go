package db_test

import (
	"context"
	"testing"

	"github.com/meridianhub/meridian/shared/platform/db"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequirePgxTenantContext_RejectsEmptyContext(t *testing.T) {
	t.Parallel()

	_, err := db.RequirePgxTenantContext(context.Background())

	require.Error(t, err)
	assert.ErrorIs(t, err, db.ErrPgxTenantContextRequired)
}

func TestRequirePgxTenantContext_RejectsEmptyTenantID(t *testing.T) {
	t.Parallel()

	ctx := tenant.WithTenant(context.Background(), tenant.TenantID(""))

	_, err := db.RequirePgxTenantContext(ctx)

	require.Error(t, err)
	assert.ErrorIs(t, err, db.ErrPgxTenantContextRequired)
}

func TestRequirePgxTenantContext_AcceptsTenantContext(t *testing.T) {
	t.Parallel()

	ctx := tenant.WithTenant(context.Background(), tenant.TenantID("acme_bank"))

	tid, err := db.RequirePgxTenantContext(ctx)

	require.NoError(t, err)
	assert.Equal(t, tenant.TenantID("acme_bank"), tid)
}

func TestRequirePgxTenantContext_AcceptsBypass(t *testing.T) {
	t.Parallel()

	ctx := db.WithPgxTenantBypass(context.Background())

	tid, err := db.RequirePgxTenantContext(ctx)

	require.NoError(t, err)
	assert.Equal(t, tenant.TenantID(""), tid)
}

func TestRequirePgxTenantContext_BypassTakesPrecedenceOverEmptyTenant(t *testing.T) {
	t.Parallel()

	// Context has empty tenant ID AND bypass — bypass should win
	ctx := tenant.WithTenant(context.Background(), tenant.TenantID(""))
	ctx = db.WithPgxTenantBypass(ctx)

	_, err := db.RequirePgxTenantContext(ctx)

	require.NoError(t, err)
}
