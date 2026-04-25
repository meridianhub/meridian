package tenant

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithTenant_and_FromContext(t *testing.T) {
	tid := TenantID("acme_corp")
	ctx := WithTenant(context.Background(), tid)

	got, ok := FromContext(ctx)
	assert.True(t, ok)
	assert.Equal(t, tid, got)
}

func TestFromContext_missing(t *testing.T) {
	got, ok := FromContext(context.Background())
	assert.False(t, ok)
	assert.Equal(t, TenantID(""), got)
}

func TestFromContext_nil_context(t *testing.T) {
	//nolint:staticcheck // SA1012: intentionally testing nil context handling
	got, ok := FromContext(nil)
	assert.False(t, ok)
	assert.Equal(t, TenantID(""), got)
}

func TestWithSlug_and_SlugFromContext(t *testing.T) {
	ctx := WithSlug(context.Background(), "volterra-energy")

	slug, ok := SlugFromContext(ctx)
	assert.True(t, ok)
	assert.Equal(t, "volterra-energy", slug)
}

func TestSlugFromContext_missing(t *testing.T) {
	slug, ok := SlugFromContext(context.Background())
	assert.False(t, ok)
	assert.Equal(t, "", slug)
}

func TestSlugFromContext_nil_context(t *testing.T) {
	//nolint:staticcheck // SA1012: intentionally testing nil context handling
	slug, ok := SlugFromContext(nil)
	assert.False(t, ok)
	assert.Equal(t, "", slug)
}

func TestWithDisplayName_and_DisplayNameFromContext(t *testing.T) {
	ctx := WithDisplayName(context.Background(), "Volterra Energy")

	name, ok := DisplayNameFromContext(ctx)
	assert.True(t, ok)
	assert.Equal(t, "Volterra Energy", name)
}

func TestDisplayNameFromContext_missing(t *testing.T) {
	name, ok := DisplayNameFromContext(context.Background())
	assert.False(t, ok)
	assert.Equal(t, "", name)
}

func TestDisplayNameFromContext_nil_context(t *testing.T) {
	//nolint:staticcheck // SA1012: intentionally testing nil context handling
	name, ok := DisplayNameFromContext(nil)
	assert.False(t, ok)
	assert.Equal(t, "", name)
}

func TestWithStatus_and_StatusFromContext(t *testing.T) {
	ctx := WithStatus(context.Background(), "provisioning")

	status, ok := StatusFromContext(ctx)
	assert.True(t, ok)
	assert.Equal(t, "provisioning", status)
}

func TestStatusFromContext_missing(t *testing.T) {
	status, ok := StatusFromContext(context.Background())
	assert.False(t, ok)
	assert.Equal(t, "", status)
}

func TestStatusFromContext_nil_context(t *testing.T) {
	//nolint:staticcheck // SA1012: intentionally testing nil context handling
	status, ok := StatusFromContext(nil)
	assert.False(t, ok)
	assert.Equal(t, "", status)
}

func TestMustFromContext_success(t *testing.T) {
	tid := TenantID("acme_corp")
	ctx := WithTenant(context.Background(), tid)

	got := MustFromContext(ctx)
	assert.Equal(t, tid, got)
}

func TestMustFromContext_panics_when_missing(t *testing.T) {
	assert.Panics(t, func() {
		MustFromContext(context.Background())
	})
}

func TestRequireFromContext_success(t *testing.T) {
	tid := TenantID("acme_corp")
	ctx := WithTenant(context.Background(), tid)

	got, err := RequireFromContext(ctx)
	require.NoError(t, err)
	assert.Equal(t, tid, got)
}

func TestRequireFromContext_error_when_missing(t *testing.T) {
	_, err := RequireFromContext(context.Background())
	assert.ErrorIs(t, err, ErrMissingTenantContext)
}

func TestPropagateToBackground_with_tenant(t *testing.T) {
	tid := TenantID("acme_corp")
	parent := WithTenant(context.Background(), tid)
	parent = WithSlug(parent, "acme-corp")
	parent = WithDisplayName(parent, "Acme Corp")
	parent = WithStatus(parent, "active")

	asyncCtx := PropagateToBackground(parent)

	// Tenant propagated
	got, ok := FromContext(asyncCtx)
	assert.True(t, ok)
	assert.Equal(t, tid, got)

	// Slug propagated
	slug, ok := SlugFromContext(asyncCtx)
	assert.True(t, ok)
	assert.Equal(t, "acme-corp", slug)

	// Display name propagated
	name, ok := DisplayNameFromContext(asyncCtx)
	assert.True(t, ok)
	assert.Equal(t, "Acme Corp", name)

	// Status propagated
	status, ok := StatusFromContext(asyncCtx)
	assert.True(t, ok)
	assert.Equal(t, "active", status)

	// No deadline from parent
	_, hasDeadline := asyncCtx.Deadline()
	assert.False(t, hasDeadline)
}

func TestPropagateToBackground_without_tenant(t *testing.T) {
	asyncCtx := PropagateToBackground(context.Background())

	got, ok := FromContext(asyncCtx)
	assert.False(t, ok)
	assert.Equal(t, TenantID(""), got)
}

func TestPropagateToBackground_does_not_carry_cancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	parent = WithTenant(parent, TenantID("acme"))
	cancel()

	asyncCtx := PropagateToBackground(parent)

	// Parent is cancelled but propagated context is not
	assert.Error(t, parent.Err())
	assert.NoError(t, asyncCtx.Err())

	// Tenant still propagated
	got, ok := FromContext(asyncCtx)
	assert.True(t, ok)
	assert.Equal(t, TenantID("acme"), got)
}
