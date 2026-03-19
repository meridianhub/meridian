package tenant_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/meridianhub/meridian/shared/platform/tenant"
)

func TestNewTenantID_Valid(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"lowercase", "acme"},
		{"uppercase", "ACME"},
		{"mixed_case", "AcmeBank"},
		{"with_numbers", "bank123"},
		{"with_underscore", "acme_bank"},
		{"single_char", "a"},
		{"max_length", strings.Repeat("a", 50)},
		{"all_numbers", "12345"},
		{"complex", "Org_123_ABC"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tenantID, err := tenant.NewTenantID(tt.input)
			if err != nil {
				t.Errorf("NewTenantID(%q) returned unexpected error: %v", tt.input, err)
			}
			if tenantID.String() != tt.input {
				t.Errorf("NewTenantID(%q).String() = %q, want %q", tt.input, tenantID.String(), tt.input)
			}
		})
	}
}

func TestNewTenantID_Invalid(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"with_hyphen", "acme-bank"},
		{"with_space", "acme bank"},
		{"with_special_chars", "acme@bank"},
		{"with_dot", "acme.bank"},
		{"too_long", strings.Repeat("a", 51)},
		{"with_slash", "acme/bank"},
		{"unicode", "acme_bänk"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tenantID, err := tenant.NewTenantID(tt.input)
			if err == nil {
				t.Errorf("NewTenantID(%q) expected error, got nil", tt.input)
			}
			if !errors.Is(err, tenant.ErrInvalidTenantID) {
				t.Errorf("NewTenantID(%q) error = %v, want ErrInvalidTenantID", tt.input, err)
			}
			if !tenantID.IsEmpty() {
				t.Errorf("NewTenantID(%q) returned non-empty TenantID on error", tt.input)
			}
		})
	}
}

func TestMustNewTenantID_Valid(t *testing.T) {
	tenantID := tenant.MustNewTenantID("valid_tenant")
	if tenantID.String() != "valid_tenant" {
		t.Errorf("MustNewTenantID(\"valid_tenant\").String() = %q, want \"valid_tenant\"", tenantID.String())
	}
}

func TestMustNewTenantID_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustNewTenantID with invalid input did not panic")
		}
	}()

	tenant.MustNewTenantID("invalid-tenant")
}

func TestTenantID_SchemaName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"acme_bank", "org_acme_bank"},
		{"bank123", "org_bank123"},
		{"ABC", "org_abc"}, // normalized to lowercase for PostgreSQL
		{"AcmeBank", "org_acmebank"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			tenantID := tenant.MustNewTenantID(tt.input)
			if got := tenantID.SchemaName(); got != tt.expected {
				t.Errorf("TenantID(%q).SchemaName() = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestTenantID_IsEmpty(t *testing.T) {
	var emptyTenantID tenant.TenantID
	if !emptyTenantID.IsEmpty() {
		t.Error("zero-value TenantID.IsEmpty() = false, want true")
	}

	validTenantID := tenant.MustNewTenantID("tenant")
	if validTenantID.IsEmpty() {
		t.Error("valid TenantID.IsEmpty() = true, want false")
	}
}

func TestContextRoundTrip(t *testing.T) {
	original := tenant.MustNewTenantID("test_tenant")
	ctx := tenant.WithTenant(context.Background(), original)

	retrieved, ok := tenant.FromContext(ctx)
	if !ok {
		t.Error("FromContext returned ok=false for context with tenant")
	}
	if retrieved != original {
		t.Errorf("FromContext returned %q, want %q", retrieved, original)
	}
}

func TestFromContext_Missing(t *testing.T) {
	ctx := context.Background()

	tenantID, ok := tenant.FromContext(ctx)
	if ok {
		t.Error("FromContext returned ok=true for context without tenant")
	}
	if !tenantID.IsEmpty() {
		t.Errorf("FromContext returned non-empty TenantID %q for context without tenant", tenantID)
	}
}

func TestFromContext_NilContext(t *testing.T) {
	//nolint:staticcheck // SA1012: intentionally testing nil context handling
	tenantID, ok := tenant.FromContext(nil)
	if ok {
		t.Error("FromContext returned ok=true for nil context")
	}
	if !tenantID.IsEmpty() {
		t.Errorf("FromContext returned non-empty TenantID %q for nil context", tenantID)
	}
}

func TestMustFromContext_Success(t *testing.T) {
	expected := tenant.MustNewTenantID("test_tenant")
	ctx := tenant.WithTenant(context.Background(), expected)

	got := tenant.MustFromContext(ctx)
	if got != expected {
		t.Errorf("MustFromContext returned %q, want %q", got, expected)
	}
}

func TestMustFromContext_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustFromContext did not panic for context without tenant")
		}
	}()

	tenant.MustFromContext(context.Background())
}

func TestContextOverwrite(t *testing.T) {
	first := tenant.MustNewTenantID("first_tenant")
	second := tenant.MustNewTenantID("second_tenant")

	ctx := tenant.WithTenant(context.Background(), first)
	ctx = tenant.WithTenant(ctx, second)

	got, ok := tenant.FromContext(ctx)
	if !ok {
		t.Error("FromContext returned ok=false")
	}
	if got != second {
		t.Errorf("FromContext returned %q, want %q (the overwritten value)", got, second)
	}
}

func TestContextIsolation(t *testing.T) {
	ten := tenant.MustNewTenantID("isolated_tenant")
	parentCtx := context.Background()
	childCtx := tenant.WithTenant(parentCtx, ten)

	// Parent should not have tenant
	_, ok := tenant.FromContext(parentCtx)
	if ok {
		t.Error("Parent context should not have tenant after child context is created")
	}

	// Child should have tenant
	got, ok := tenant.FromContext(childCtx)
	if !ok {
		t.Error("Child context should have tenant")
	}
	if got != ten {
		t.Errorf("Child context has wrong tenant: got %q, want %q", got, ten)
	}
}

func TestRequireFromContext_Success(t *testing.T) {
	expected := tenant.MustNewTenantID("test_tenant")
	ctx := tenant.WithTenant(context.Background(), expected)

	got, err := tenant.RequireFromContext(ctx)
	if err != nil {
		t.Errorf("RequireFromContext returned unexpected error: %v", err)
	}
	if got != expected {
		t.Errorf("RequireFromContext returned %q, want %q", got, expected)
	}
}

func TestRequireFromContext_Missing(t *testing.T) {
	ctx := context.Background()

	tenantID, err := tenant.RequireFromContext(ctx)
	if err == nil {
		t.Error("RequireFromContext expected error for context without tenant")
	}
	if !errors.Is(err, tenant.ErrMissingTenantContext) {
		t.Errorf("RequireFromContext error = %v, want ErrMissingTenantContext", err)
	}
	if !tenantID.IsEmpty() {
		t.Errorf("RequireFromContext returned non-empty TenantID %q on error", tenantID)
	}
}

func TestRequireFromContext_NilContext(t *testing.T) {
	//nolint:staticcheck // SA1012: intentionally testing nil context handling
	tenantID, err := tenant.RequireFromContext(nil)
	if err == nil {
		t.Error("RequireFromContext expected error for nil context")
	}
	if !errors.Is(err, tenant.ErrMissingTenantContext) {
		t.Errorf("RequireFromContext error = %v, want ErrMissingTenantContext", err)
	}
	if !tenantID.IsEmpty() {
		t.Errorf("RequireFromContext returned non-empty TenantID %q for nil context", tenantID)
	}
}

func TestPropagateToBackground_WithTenant(t *testing.T) {
	expected := tenant.MustNewTenantID("async_tenant")
	parentCtx := tenant.WithTenant(context.Background(), expected)

	// PropagateToBackground should create a new context with just the tenant
	asyncCtx := tenant.PropagateToBackground(parentCtx)

	// Verify tenant was propagated
	got, ok := tenant.FromContext(asyncCtx)
	if !ok {
		t.Error("PropagateToBackground did not propagate tenant to new context")
	}
	if got != expected {
		t.Errorf("PropagateToBackground propagated wrong tenant: got %q, want %q", got, expected)
	}

	// Verify it's a fresh context (no deadline from parent)
	if deadline, hasDeadline := asyncCtx.Deadline(); hasDeadline {
		t.Errorf("PropagateToBackground context should not have deadline, got %v", deadline)
	}
}

func TestPropagateToBackground_WithoutTenant(t *testing.T) {
	parentCtx := context.Background()

	// PropagateToBackground should return a plain background context
	asyncCtx := tenant.PropagateToBackground(parentCtx)

	// Verify no tenant
	_, ok := tenant.FromContext(asyncCtx)
	if ok {
		t.Error("PropagateToBackground should not have tenant when parent has none")
	}
}

func TestPropagateToBackground_NilContext(t *testing.T) {
	//nolint:staticcheck // SA1012: intentionally testing nil context handling
	asyncCtx := tenant.PropagateToBackground(nil)

	// Should return a valid background context
	if asyncCtx == nil {
		t.Error("PropagateToBackground(nil) returned nil context")
	}

	// Verify no tenant
	_, ok := tenant.FromContext(asyncCtx)
	if ok {
		t.Error("PropagateToBackground(nil) should not have tenant")
	}
}

func TestSlugContextRoundTrip(t *testing.T) {
	ctx := tenant.WithSlug(context.Background(), "volterra-energy")

	slug, ok := tenant.SlugFromContext(ctx)
	if !ok {
		t.Error("SlugFromContext returned ok=false for context with slug")
	}
	if slug != "volterra-energy" {
		t.Errorf("SlugFromContext returned %q, want %q", slug, "volterra-energy")
	}
}

func TestSlugFromContext_Missing(t *testing.T) {
	slug, ok := tenant.SlugFromContext(context.Background())
	if ok {
		t.Error("SlugFromContext returned ok=true for context without slug")
	}
	if slug != "" {
		t.Errorf("SlugFromContext returned %q, want empty string", slug)
	}
}

func TestSlugFromContext_NilContext(t *testing.T) {
	//nolint:staticcheck // SA1012: intentionally testing nil context handling
	slug, ok := tenant.SlugFromContext(nil)
	if ok {
		t.Error("SlugFromContext returned ok=true for nil context")
	}
	if slug != "" {
		t.Errorf("SlugFromContext returned %q for nil context, want empty string", slug)
	}
}

func TestSlugAndTenantIDIndependent(t *testing.T) {
	tid := tenant.MustNewTenantID("volterra_energy")
	ctx := tenant.WithTenant(context.Background(), tid)
	ctx = tenant.WithSlug(ctx, "volterra-energy")

	gotID, ok := tenant.FromContext(ctx)
	if !ok || gotID != tid {
		t.Errorf("FromContext returned (%q, %v), want (%q, true)", gotID, ok, tid)
	}

	gotSlug, ok := tenant.SlugFromContext(ctx)
	if !ok || gotSlug != "volterra-energy" {
		t.Errorf("SlugFromContext returned (%q, %v), want (%q, true)", gotSlug, ok, "volterra-energy")
	}
}
